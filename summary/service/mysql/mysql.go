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

package mysql

import (
	"github.com/percona/cloud-protocol/proto"
	"github.com/percona/percona-agent/pct"
	"github.com/percona/percona-agent/summary/cmd"
)

const (
	SERVICE_NAME = "system"
)

type MySQL struct {
	logger *pct.Logger
	bindir string
}

func NewMySQL(logger *pct.Logger, bindir string) *MySQL {
	return &MySQL{
		logger: logger,
		bindir: bindir,
	}
}

/////////////////////////////////////////////////////////////////////////////
// Interface
/////////////////////////////////////////////////////////////////////////////

func (s *MySQL) Handle(protoCmd *proto.Cmd) *proto.Reply {
	ptMySQLSummary := cmd.New("pt-mysql-summary")
	output, err := ptMySQLSummary.Run()
	if err != nil {
		s.logger.Error("summary %s: %s", SERVICE_NAME, err)
	}

	return protoCmd.Reply(output, err)
}
