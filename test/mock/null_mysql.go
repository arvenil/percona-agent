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

package mock

import (
	"database/sql"
	"github.com/percona/cloud-protocol/proto"
	"github.com/percona/percona-agent/mysql"
)

type NullMySQL struct {
	set         []mysql.Query
	explain     map[string]*proto.ExplainResult
	uptime      int64
	uptimeCount uint
}

func NewNullMySQL() *NullMySQL {
	n := &NullMySQL{
		set:     []mysql.Query{},
		explain: make(map[string]*proto.ExplainResult),
	}
	return n
}

func (n *NullMySQL) DB() *sql.DB {
	return nil
}

func (n *NullMySQL) DSN() string {
	return "user:pass@tcp(127.0.0.1:3306)/?parseTime=true"
}

func (n *NullMySQL) Connect(tries uint) error {
	return nil
}

func (n *NullMySQL) Close() {
	return
}

func (n *NullMySQL) Explain(query string, db string) (explain *proto.ExplainResult, err error) {
	return n.explain[query], nil
}

func (n *NullMySQL) SetExplain(query string, explain *proto.ExplainResult) {
	n.explain[query] = explain
}

func (n *NullMySQL) Set(queries []mysql.Query) error {
	for _, q := range queries {
		n.set = append(n.set, q)
	}
	return nil
}

func (n *NullMySQL) GetSet() []mysql.Query {
	return n.set
}

func (n *NullMySQL) Reset() {
	n.set = nil
}

func (n *NullMySQL) GetGlobalVarString(varName string) string {
	return ""
}

func (n *NullMySQL) Uptime() int64 {
	n.uptimeCount++
	return n.uptime
}

func (n *NullMySQL) GetUptimeCount() uint {
	return n.uptimeCount
}

func (n *NullMySQL) SetUptime(uptime int64) {
	n.uptime = uptime
}
