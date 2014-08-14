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

package system_test

import (
	"encoding/json"
	"github.com/percona/cloud-protocol/proto"
	"github.com/percona/percona-agent/instance"
	"github.com/percona/percona-agent/pct"
	"github.com/percona/percona-agent/summary/service/system"
	"github.com/percona/percona-agent/test"
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

type ManagerTestSuite struct {
	logChan       chan *proto.LogEntry
	logger        *pct.Logger
	tickChan      chan time.Time
	readyChan     chan bool
	configDir     string
	binDir        string
	tmpDir        string
	dsn           string
	rir           *instance.Repo
	mysqlInstance proto.ServiceInstance
	api           *mock.API
}

var _ = Suite(&ManagerTestSuite{})

func (s *ManagerTestSuite) SetUpSuite(t *C) {
	s.dsn = os.Getenv("PCT_TEST_MYSQL_DSN")
	if s.dsn == "" {
		t.Fatal("PCT_TEST_MYSQL_DSN is not set")
	}

	s.logChan = make(chan *proto.LogEntry, 10)
	s.logger = pct.NewLogger(s.logChan, system.SERVICE_NAME+"-manager-test")

	var err error
	s.tmpDir, err = ioutil.TempDir("/tmp", "agent-test")
	t.Assert(err, IsNil)

	if err := pct.Basedir.Init(s.tmpDir); err != nil {
		t.Fatal(err)
	}
	s.configDir = pct.Basedir.Dir("config")
	s.binDir = pct.Basedir.Dir("bin")
	err = os.Link(test.RootDir+"/../build/pt-summary", s.binDir+"/pt-summary")
	t.Assert(err, IsNil)
	err = os.Chmod(s.binDir+"/pt-summary", 0777)
	t.Assert(err, IsNil)

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

func (s *ManagerTestSuite) SetUpTest(t *C) {
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

func (s *ManagerTestSuite) TearDownSuite(t *C) {
	if err := os.RemoveAll(s.tmpDir); err != nil {
		t.Error(err)
	}
}

// --------------------------------------------------------------------------

func (s *ManagerTestSuite) TestService(t *C) {
	// Create service
	service := system.NewSystem(s.logger, s.binDir)

	cmd := &proto.Cmd{
		Service: "Summary",
		Cmd:     "system",
	}

	gotReply := service.Handle(cmd)
	t.Assert(gotReply, NotNil)
	t.Assert(gotReply.Error, Equals, "")

	var gotResult string
	err := json.Unmarshal(gotReply.Data, &gotResult)
	t.Assert(err, IsNil)
	headers := []string{
		"# Percona Toolkit System Summary Report #####################",
		"# Processor #################################################",
		"# Memory ####################################################",
		"# Mounted Filesystems #######################################",
		"# Disk Schedulers And Queue Size ############################",
		"# Disk Partioning ###########################################",
		"# Kernel Inode State ########################################",
		"# LVM Volumes ###############################################",
		"# LVM Volume Groups #########################################",
		"# RAID Controller ###########################################",
		"# Network Config ############################################",
		"# Interface Statistics ######################################",
		"# Network Connections #######################################",
		"# Top Processes #############################################",
		"# Notable Processes #########################################",
		"# Simplified and fuzzy rounded vmstat \\(wait please\\) #########",
		"# The End ###################################################",
	}
	for i := range headers {
		t.Check(gotResult, MatchesMultiline, headers[i])

	}
}
