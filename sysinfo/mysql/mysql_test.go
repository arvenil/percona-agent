/*
   Copyright (c) 2014-2015, Percona LLC and/or its affiliates. All rights reserved.

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

package mysql_test

import (
	"encoding/json"
	"github.com/percona/cloud-protocol/proto/v1"
	"github.com/percona/percona-agent/instance"
	"github.com/percona/percona-agent/pct"
	"github.com/percona/percona-agent/sysinfo/mysql"
	. "github.com/percona/percona-agent/test/checkers"
	"github.com/percona/percona-agent/test/mock"
	. "gopkg.in/check.v1"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) { TestingT(t) }

/////////////////////////////////////////////////////////////////////////////
// Manager test suite
/////////////////////////////////////////////////////////////////////////////

type TestSuite struct {
	logChan       chan *proto.LogEntry
	logger        *pct.Logger
	tickChan      chan time.Time
	readyChan     chan bool
	configDir     string
	tmpDir        string
	dsn           string
	rir           *instance.Repo
	mysqlInstance proto.ServiceInstance
	api           *mock.API
}

var _ = Suite(&TestSuite{})

func (s *TestSuite) SetUpSuite(t *C) {
	s.dsn = os.Getenv("PCT_TEST_MYSQL_DSN")
	if s.dsn == "" {
		t.Fatal("PCT_TEST_MYSQL_DSN is not set")
	}

	s.logChan = make(chan *proto.LogEntry, 10)
	s.logger = pct.NewLogger(s.logChan, mysql.SERVICE_NAME+"-manager-test")

	var err error
	s.tmpDir, err = ioutil.TempDir("/tmp", "agent-test")
	t.Assert(err, IsNil)

	if err := pct.Basedir.Init(s.tmpDir); err != nil {
		t.Fatal(err)
	}
	s.configDir = pct.Basedir.Dir("config")

	// Real instance repo
	s.rir = instance.NewRepo(pct.NewLogger(s.logChan, "im-test"), s.configDir, s.api)
	data, err := json.Marshal(&proto.MySQLInstance{
		Hostname: "db1",
		DSN:      s.dsn,
	})
	t.Assert(err, IsNil)
	s.rir.Add("mysql", 1, data, false)
	s.mysqlInstance = proto.ServiceInstance{Service: "mysql", InstanceId: 1}

	links := map[string]string{
		"agent":     "http://localhost/agent",
		"instances": "http://localhost/instances",
	}
	s.api = mock.NewAPI("http://localhost", "http://localhost", "123", "abc-123-def", links)
}

func (s *TestSuite) SetUpTest(t *C) {
	glob := filepath.Join(pct.Basedir.Dir("config"), "*")
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *TestSuite) TearDownSuite(t *C) {
	if err := os.RemoveAll(s.tmpDir); err != nil {
		t.Error(err)
	}
}

// --------------------------------------------------------------------------

func (s *TestSuite) TestService(t *C) {
	// Create service
	service := mysql.NewMySQL(s.logger, s.rir)

	data, err := json.Marshal(&s.mysqlInstance)
	t.Assert(err, IsNil)

	cmd := &proto.Cmd{
		Service: "Summary",
		Cmd:     "mysql",
		Data:    data,
	}

	gotReply := service.Handle(cmd)
	t.Assert(gotReply, NotNil)
	t.Assert(gotReply.Error, Equals, "")

	sysinfoResult := &proto.SysinfoResult{}
	err = json.Unmarshal(gotReply.Data, &sysinfoResult)
	t.Assert(err, IsNil)
	headers := []string{
		"# Percona Toolkit MySQL Summary Report #######################",
		"# Instances ##################################################",
		"# MySQL Executable ###########################################",
		"# Report On Port [0-9]+ ########################################",
		"# Processlist ################################################",
		"# Status Counters \\(Wait " + mysql.PT_SLEEP_SECONDS + " Seconds\\) ##########################",
		"# Table cache ################################################",
		"# Key Percona Server features ################################",
		"# Percona XtraDB Cluster #####################################",
		"# Plugins ####################################################",
		"# Query cache ################################################",
		"# Schema #####################################################",
		"# Noteworthy Technologies ####################################",
		"# InnoDB #####################################################",
		"# MyISAM #####################################################",
		"# Security ###################################################",
		"# Binary Logging #############################################",
		"# Noteworthy Variables #######################################",
		"# Configuration File #########################################",
		"# The End ####################################################",
	}
	for i := range headers {
		t.Check(sysinfoResult.Raw, MatchesMultiline, headers[i])
	}
}

func (s *TestSuite) TestExecutableNotFound(t *C) {
	// Create service
	service := mysql.NewMySQL(s.logger, s.rir)
	// Fake executable name to trigger "unknown executable" error
	service.CmdName = "unknown-executable"

	data, err := json.Marshal(&s.mysqlInstance)
	t.Assert(err, IsNil)

	cmd := &proto.Cmd{
		Service: "Summary",
		Cmd:     "mysql",
		Data:    data,
	}

	gotReply := service.Handle(cmd)
	t.Assert(gotReply, NotNil)
	// Error is like code error for web-app, it depends on this string
	// changing this string means breaking contract between agent/api and web-app
	t.Assert(gotReply.Error, Equals, "Executable file not found in $PATH")
}

func (s *TestSuite) TestParsingParamsWithSocket(t *C) {
	dsn, err := mysql.NewDSN("pt-agent:PabloIsAwesome@unix(/var/lib/mysql/mysql.sock)/")
	t.Assert(err, IsNil)
	expectedArgs := []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--user", "pt-agent",
		"--password", "PabloIsAwesome",
		"--socket", "/var/lib/mysql/mysql.sock",
	}
	gotArgs := mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)
}

func (s *TestSuite) TestParsingParamsWithHostname(t *C) {
	dsn, err := mysql.NewDSN("pt-agent:PabloIsAwesome@tcp(leonardo.is.awesome.too:7777)/")
	t.Assert(err, IsNil)
	expectedArgs := []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--user", "pt-agent",
		"--password", "PabloIsAwesome",
		"--host", "leonardo.is.awesome.too",
		"--port", "7777",
	}
	gotArgs := mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)
}

func (s *TestSuite) TestParsingParamsWithHostnameAndPort(t *C) {
	dsn, err := mysql.NewDSN("pt-agent:PabloIsAwesome@tcp(leonardo.is.awesome.too:7777)/")
	t.Assert(err, IsNil)
	expectedArgs := []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--user", "pt-agent",
		"--password", "PabloIsAwesome",
		"--host", "leonardo.is.awesome.too",
		"--port", "7777",
	}
	gotArgs := mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)
}

func (s *TestSuite) TestParsingParamsWithoutPassword(t *C) {
	var dsn *mysql.DSN
	var err error
	var gotArgs, expectedArgs []string

	dsn, err = mysql.NewDSN("pt-agent@unix(/pablo/is/awesome)/")
	t.Assert(err, IsNil)
	expectedArgs = []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--user", "pt-agent",
		"--socket", "/pablo/is/awesome",
	}
	gotArgs = mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)

	dsn, err = mysql.NewDSN("pt-agent@tcp(leonardo.is.awesome.too:7777)/")
	t.Assert(err, IsNil)
	expectedArgs = []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--user", "pt-agent",
		"--host", "leonardo.is.awesome.too",
		"--port", "7777",
	}
	gotArgs = mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)
}

func (s *TestSuite) TestParsingParamsWithoutUser(t *C) {
	var dsn *mysql.DSN
	var err error
	var gotArgs, expectedArgs []string

	dsn, err = mysql.NewDSN(":LukaszIsUberAwesome@unix(/pablo/is/awesome)/")
	t.Assert(err, IsNil)
	expectedArgs = []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--password", "LukaszIsUberAwesome",
		"--socket", "/pablo/is/awesome",
	}
	gotArgs = mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)

	dsn, err = mysql.NewDSN(":LukaszIsUberAwesome@tcp(leonardo.is.awesome.too:7777)/")
	t.Assert(err, IsNil)
	expectedArgs = []string{
		"--sleep", mysql.PT_SLEEP_SECONDS,
		"--password", "LukaszIsUberAwesome",
		"--host", "leonardo.is.awesome.too",
		"--port", "7777",
	}
	gotArgs = mysql.CreateParamsForPtMySQLSummary(dsn)
	t.Assert(gotArgs, DeepEquals, expectedArgs)
}
