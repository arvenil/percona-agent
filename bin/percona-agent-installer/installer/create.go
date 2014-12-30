/*
   Copyright (c) 2014, Percona LLC and/or its affiliates. All rights reserved.

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package installer

import (
	"encoding/json"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/percona/cloud-protocol/proto"
	"github.com/percona/percona-agent/agent"
	"github.com/percona/percona-agent/instance"
	"github.com/percona/percona-agent/mysql"
	"github.com/percona/percona-agent/pct"
	"log"
	"math/rand"
	"net/http"
)

func (i *Installer) createMySQLUser(dsn mysql.DSN) (mysql.DSN, error) {
	// Same host:port or socket, but different user and pass.
	userDSN := dsn
	userDSN.Username = "percona-agent"
	userDSN.Password = fmt.Sprintf("%p%d", &dsn, rand.Uint32())
	userDSN.OldPasswords = i.flags.Bool["old-passwords"]

	dsnString, _ := dsn.DSN()
	conn := mysql.NewConnection(dsnString)
	if err := conn.Connect(1); err != nil {
		return userDSN, err
	}
	defer conn.Close()
	grants := MakeGrant(dsn, userDSN.Username, userDSN.Password, i.flags.Int64["mysql-max-user-connections"])
	for _, grant := range grants {
		if i.flags.Bool["debug"] {
			log.Println(grant)
		}
		_, err := conn.DB().Exec(grant)
		if err != nil {
			return userDSN, fmt.Errorf("Error executing %s: %s", grant, err)
		}
	}

	// Go MySQL driver resolves localhost to 127.0.0.1 but localhost is a special
	// value for MySQL, so 127.0.0.1 may not work with a grant @localhost, so we
	// add a 2nd grant @127.0.0.1 to be sure.
	if dsn.Hostname == "localhost" {
		dsn2 := dsn
		dsn2.Hostname = "127.0.0.1"
		grants := MakeGrant(dsn2, userDSN.Username, userDSN.Password, i.flags.Int64["mysql-max-user-connections"])
		for _, grant := range grants {
			if i.flags.Bool["debug"] {
				log.Println(grant)
			}
			_, err := conn.DB().Exec(grant)
			if err != nil {
				return userDSN, fmt.Errorf("Error executing %s: %s", grant, err)
			}
		}
	}

	return userDSN, nil
}

func (i *Installer) createServerInstance() (*proto.ServerInstance, error) {
	// POST <api>/instances/server
	si := &proto.ServerInstance{
		Hostname: i.hostname,
	}
	data, err := json.Marshal(si)
	if err != nil {
		return nil, err
	}
	url := pct.URL(i.agentConfig.ApiHostname, "instances", "server")
	if i.flags.Bool["debug"] {
		log.Println(url)
	}
	resp, _, err := i.api.Post(i.agentConfig.ApiKey, url, data)
	if i.flags.Bool["debug"] {
		log.Printf("resp=%#v\n", resp)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}
	// Create new instance, if it already exist then just use it
	// todo: better handling of duplicate instance
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return nil, fmt.Errorf("Failed to create server instance (status code %d)", resp.StatusCode)
	}

	// API returns URI of new resource in Location header
	uri := resp.Header.Get("Location")
	if uri == "" {
		return nil, fmt.Errorf("API did not return location of new server instance")
	}

	// GET <api>/instances/server/id (URI)
	code, data, err := i.api.Get(i.agentConfig.ApiKey, uri)
	if i.flags.Bool["debug"] {
		log.Printf("code=%d\n", code)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("Failed to get new server instance (status code %d)", code)
	}
	if err := json.Unmarshal(data, si); err != nil {
		return nil, fmt.Errorf("Failed to parse server instance entity: %s", err)
	}
	return si, nil
}

func (i *Installer) createMySQLInstance(dsn mysql.DSN) (*proto.MySQLInstance, error) {
	// First use instance.Manager to fill in details about the MySQL server.
	dsnString, _ := dsn.DSN()
	mi := &proto.MySQLInstance{
		Hostname: i.hostname,
		DSN:      dsnString,
	}
	if err := instance.GetMySQLInfo(mi); err != nil {
		if i.flags.Bool["debug"] {
			log.Printf("err=%s\n", err)
		}
		return nil, err
	}

	// POST <api>/instances/mysql
	data, err := json.Marshal(mi)
	if err != nil {
		return nil, err
	}
	url := pct.URL(i.agentConfig.ApiHostname, "instances", "mysql")
	if i.flags.Bool["debug"] {
		log.Println(url)
	}
	resp, _, err := i.api.Post(i.agentConfig.ApiKey, url, data)
	if i.flags.Bool["debug"] {
		log.Printf("resp=%#v\n", resp)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}

	// Create new instance, if it already exist then update it
	if resp.StatusCode == http.StatusConflict {
		// API returns URI of existing resource in Location header
		uri := resp.Header.Get("Location")
		if uri == "" {
			return nil, fmt.Errorf("API did not return location of exisiting MySQL instance")
		}

		resp, _, err := i.api.Put(i.agentConfig.ApiKey, uri, data)
		if i.flags.Bool["debug"] {
			log.Printf("resp=%#v\n", resp)
			log.Printf("err=%s\n", err)
		}
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Failed to update MySQL instance (status code %d)", resp.StatusCode)
		}
	} else if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Failed to create MySQL instance (status code %d)", resp.StatusCode)
	}

	// API returns URI of new (or already existing one) resource in Location header
	uri := resp.Header.Get("Location")
	if uri == "" {
		return nil, fmt.Errorf("API did not return location of new MySQL instance")
	}

	// GET <api>/instances/mysql/id (URI)
	code, data, err := i.api.Get(i.agentConfig.ApiKey, uri)
	if i.flags.Bool["debug"] {
		log.Printf("code=%d\n", code)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("Failed to get new MySQL instance (status code %d)", code)
	}
	if err := json.Unmarshal(data, mi); err != nil {
		return nil, fmt.Errorf("Failed to parse MySQL instance entity: %s", err)
	}
	return mi, nil
}

func (i *Installer) createAgent(configs []proto.AgentConfig) (*proto.Agent, error) {
	agent := &proto.Agent{
		Hostname: i.hostname,
		Version:  agent.VERSION,
		Configs:  configs,
	}
	data, err := json.Marshal(agent)
	if err != nil {
		return nil, err
	}
	url := pct.URL(i.agentConfig.ApiHostname, "agents")
	if i.flags.Bool["debug"] {
		log.Println(url)
	}
	resp, _, err := i.api.Post(i.agentConfig.ApiKey, url, data)
	if i.flags.Bool["debug"] {
		log.Printf("resp=%#v\n", resp)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusConflict {
		// agent was created or already exist - either is ok, continue
	} else if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-Percona-Agents-Limit") != "" {
		return nil, fmt.Errorf(
			"Maximum number of %s agents exceeded.\n"+
				"Go to https://cloud.percona.com/agents and remove unused agents or contact Percona to increase limit.",
			resp.Header.Get("X-Percona-Agents-Limit"),
		)
	} else {
		return nil, fmt.Errorf("Failed to create agent instance (status code %d)", resp.StatusCode)
	}

	// API returns URI of new resource in Location header
	uri := resp.Header.Get("Location")
	if uri == "" {
		return nil, fmt.Errorf("API did not return location of new agent")
	}

	// GET <api>/agents/:uuid
	code, data, err := i.api.Get(i.agentConfig.ApiKey, uri)
	if i.flags.Bool["debug"] {
		log.Printf("code=%d\n", code)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("Failed to get new agent (status code %d)", code)
	}
	if err := json.Unmarshal(data, agent); err != nil {
		return nil, fmt.Errorf("Failed to parse agent entity: %s", err)
	}
	return agent, nil
}

func (i *Installer) updateAgent(uuid string) (*proto.Agent, error) {
	agent := &proto.Agent{
		Uuid:     uuid,
		Hostname: i.hostname,
		Version:  agent.VERSION,
	}
	data, err := json.Marshal(agent)
	if err != nil {
		return nil, err
	}
	url := pct.URL(i.agentConfig.ApiHostname, "agents", uuid)
	if i.flags.Bool["debug"] {
		log.Println(url)
		log.Println(string(data))
	}
	resp, _, err := i.api.Put(i.agentConfig.ApiKey, url, data)
	if i.flags.Bool["debug"] {
		log.Printf("resp=%#v\n", resp)
		log.Printf("err=%s\n", err)
	}
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Failed to update agent via API (status code %d)", resp.StatusCode)
	}
	return agent, nil
}
