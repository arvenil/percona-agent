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

package main

import (
	"flag"
	"fmt"
	"github.com/percona/percona-agent/agent"
	"github.com/percona/percona-agent/pct"
	"log"
	"os"
	"os/user"
)

const (
	DEFAULT_APP_HOSTNAME = "https://cloud.percona.com"
)

var (
	flagApiHostname          string
	flagApiKey               string
	flagBasedir              string
	flagDebug                bool
	flagCreateMySQLUser      bool
	flagCreateMySQLInstance  bool
	flagCreateServerInstance bool
	flagStartServices        bool
	flagCreateAgent          bool
	flagStartMySQLServices   bool
	flagMySQL                bool
	flagOldPasswords         bool
	flagPlainPasswords       bool
	flagNonInteractive       bool
	flagMySQLUser            string
	flagMySQLPass            string
	flagMySQLHost            string
	flagMySQLPort            string
	flagMySQLSocket          string
)

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	flag.StringVar(&flagApiHostname, "api-host", agent.DEFAULT_API_HOSTNAME, "API host")
	flag.StringVar(&flagApiKey, "api-key", "", "API key, it is available at "+DEFAULT_APP_HOSTNAME+"/api-key")
	flag.StringVar(&flagBasedir, "basedir", pct.DEFAULT_BASEDIR, "Agent basedir")
	flag.BoolVar(&flagDebug, "debug", false, "Debug")
	// --
	flag.BoolVar(&flagMySQL, "mysql", true, "Install for MySQL")
	flag.BoolVar(&flagCreateMySQLInstance, "create-mysql-instance", true, "Create MySQL instance")
	flag.BoolVar(&flagCreateServerInstance, "create-server-instance", true, "Create server instance")
	flag.BoolVar(&flagStartServices, "start-services", true, "Start all services")
	flag.BoolVar(&flagStartMySQLServices, "start-mysql-services", true, "Start MySQL services")
	flag.BoolVar(&flagCreateAgent, "create-agent", true, "Create agent")
	flag.BoolVar(&flagOldPasswords, "old-passwords", false, "Old passwords")
	flag.BoolVar(&flagPlainPasswords, "plain-passwords", false, "Plain passwords") // @todo: Workaround used in tests for "stty: standard input: Inappropriate ioctl for device"
	flag.BoolVar(&flagNonInteractive, "non-interactive", false, "Non-interactive mode for headless installation")
	flag.BoolVar(&flagCreateMySQLUser, "create-mysql-user", true, "Create MySQL user for agent (used with -non-interactive=true mode)")
	username := ""
	currentUser, _ := user.Current()
	if currentUser != nil {
		username = currentUser.Username
	}
	flag.StringVar(&flagMySQLUser, "mysql-user", username, "MySQL username (used with -non-interactive=true mode)")
	flag.StringVar(&flagMySQLPass, "mysql-pass", "", "MySQL password (used with -non-interactive=true mode)")
	flag.StringVar(&flagMySQLHost, "mysql-host", "localhost", "MySQL host (used with -non-interactive=true mode)")
	flag.StringVar(&flagMySQLPort, "mysql-port", "3306", "MySQL port (used with -non-interactive=true mode)")
	flag.StringVar(&flagMySQLSocket, "mysql-socket", "", "MySQL socket file (used with -non-interactive=true mode)")
}

func main() {
	flag.Parse()

	agentConfig := &agent.Config{
		ApiHostname: flagApiHostname,
		ApiKey:      flagApiKey,
	}
	// todo: do flags a better way
	if !flagMySQL {
		flagCreateMySQLInstance = false
		flagStartMySQLServices = false
	}

	if flagMySQLSocket != "" && flagMySQLHost != "" {
		log.Println("Options -mysql-socket and -mysql-host are exclusive\n")
		os.Exit(1)
	}

	if flagMySQLSocket != "" && flagMySQLPort != "" {
		log.Println("Options -mysql-socket and -mysql-port are exclusive\n")
		os.Exit(1)
	}

	flags := Flags{
		Bool: map[string]bool{
			"debug":                  flagDebug,
			"create-server-instance": flagCreateServerInstance,
			"start-services":         flagStartServices,
			"create-mysql-user":      flagCreateMySQLUser,
			"create-mysql-instance":  flagCreateMySQLInstance,
			"start-mysql-services":   flagStartMySQLServices,
			"create-agent":           flagCreateAgent,
			"old-passwords":          flagOldPasswords,
			"plain-passwords":        flagPlainPasswords,
			"non-interactive":        flagNonInteractive,
		},
		String: map[string]string{
			"app-host":     DEFAULT_APP_HOSTNAME,
			"mysql-user":   flagMySQLUser,
			"mysql-pass":   flagMySQLPass,
			"mysql-host":   flagMySQLHost,
			"mysql-port":   flagMySQLPort,
			"mysql-socket": flagMySQLSocket,
		},
	}

	// Agent stores all its files in the basedir.  This must be called first
	// because installer uses pct.Basedir and assumes it's already initialized.
	if err := pct.Basedir.Init(flagBasedir); err != nil {
		log.Printf("Error initializing basedir %s: %s\n", flagBasedir, err)
		os.Exit(1)
	}

	installer := NewInstaller(NewTerminal(os.Stdin, flags), flagBasedir, pct.NewAPI(), agentConfig, flags)
	fmt.Println("CTRL-C at any time to quit")
	// todo: catch SIGINT and clean up
	if err := installer.Run(); err != nil {
		fmt.Println(err)
		fmt.Println("Install failed")
		os.Exit(1)
	}
	fmt.Println("Install successful")
	os.Exit(0)
}
