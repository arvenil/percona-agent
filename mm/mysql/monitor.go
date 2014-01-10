package mysql

import (
	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"strconv"
	"strings"
	"time"
	"github.com/percona/cloud-tools/mm"
	pct "github.com/percona/cloud-tools"
	"github.com/percona/cloud-tools/eventicker"
)

type Monitor struct {
	config        *Config
	logger        *pct.Logger
	ticker        *eventicker.EvenTicker
	collectionChan      chan *mm.Collection
	// --
	conn          *sql.DB
	connected     bool
	connectedChan chan bool
	status        *pct.Status
	backoff       *pct.Backoff
	sync          *pct.SyncChan
	stats map[string]bool
}

func NewMonitor(config *Config, logger *pct.Logger, ticker *eventicker.EvenTicker, collectionChan chan *mm.Collection) *Monitor {
	stats := make(map[string]bool)
	for _, stat := range config.Status {
		stats[stat] = true
	}

	m := &Monitor{
		config: config,
		logger: logger,
		ticker: ticker,
		collectionChan: collectionChan,
		// --
		connectedChan: make(chan bool, 1),
		status: pct.NewStatus([]string{"mysql-monitor"}),
		backoff: pct.NewBackoff(5 * time.Second),
		sync: pct.NewSyncChan(),
		stats: stats,
	}
	return m
}

/////////////////////////////////////////////////////////////////////////////
// Interface
/////////////////////////////////////////////////////////////////////////////

func (m *Monitor) Start() {
}

func (m *Monitor) Stop() {
}

func (m *Monitor) Status() string {
	return m.status.Get("mysql-monitor", true)  // true = read lock
}

/////////////////////////////////////////////////////////////////////////////
// Implementation
/////////////////////////////////////////////////////////////////////////////

// @goroutine
func (m *Monitor) run() {
	go m.connect()
	defer func() {
		if m.conn != nil {
			m.conn.Close()
		}
		m.status.Update("mysql-monistor", "Stopped")
	}()

	prefix := "mysql/"
	if m.config.InstanceName != "" {
		prefix = prefix + m.config.InstanceName + "/"
	}

	ticker := m.ticker.Sync(time.Now().UnixNano())

	for {
		select {
		case now := <-ticker.C:
			if m.connected {
				c := &mm.Collection{
					StartTs: now.Unix(),
					Metrics: []mm.Metric{},
				}

				// Get collection of metrics.
				m.GetShowStatusMetrics(m.conn, prefix, c)
				if m.config.InnoDBMetrics != "" {
					m.GetInnoDBMetrics(m.conn, prefix, c)
				}
				if m.config.Userstats {
					m.GetTableStatMetrics(m.conn, prefix, c, m.config.UserstatsIgnoreDb)
					m.GetIndexStatMetrics(m.conn, prefix, c, m.config.UserstatsIgnoreDb)
				}

				// Send the metrics (to an mm.Aggregator).
				if len(c.Metrics) > 0 {
					select {
					case m.collectionChan <- c:
					case <-time.After(500 * time.Millisecond):
						// lost collection
						m.logger.Debug("Lost MySQL metrics; timeout spooling after 500ms")
					}
				} else {
					m.logger.Debug("No metrics")
				}
			} else {
				m.logger.Debug("Not connected")
			}
		case connected := <-m.connectedChan:
			m.connected = connected
			if connected {
				m.status.Update("mysql-monitor", "Running")
			} else {
				go m.connect()
			}
		case <-m.sync.StopChan:
			return
		}
	}
}

// @goroutine
func (m *Monitor) connect() {
	m.status.Update("mysql-monitor", "Connecting to MySQL")

	// Close/release previous connection, if any.
	if m.conn != nil {
		m.conn.Close()
	}

	// Try forever to connect to MySQL...
	for m.conn == nil {

		// Wait between connect attempts.
		time.Sleep(m.backoff.Wait())

		// Open connection to MySQL but...
		db, err := sql.Open("mysql", m.config.DSN)
		if err != nil {
			m.logger.Error("sql.Open error: ", err)
			continue
		}

		// ...try to use the connection for real.
		if err := m.conn.Ping(); err != nil {
			// Connection failed.  Wrong username or password?
			m.logger.Warn(err)
			m.conn.Close()
			m.conn = nil
			continue
		}

		// Connected
		m.conn = db
		m.backoff.Success()

		// Set global vars we need.  If these fail, that's ok: they won't work,
		// but don't let that stop us from collecting other metrics.
		if m.config.InnoDBMetrics != "" {
			_, err := db.Exec("SET GLOBAL innodb_monitor_enable = \"" + m.config.InnoDBMetrics + "\"")
			if err != nil {
				m.logger.Error("Failed to enable InnoDB metrics ", m.config.InnoDBMetrics, ": ", err)
			}
		}
		if m.config.Userstats {
			_, err := db.Exec("SET GLOBAL userstat=ON")
			if err != nil {
				m.logger.Error("Failed to enable userstats: ", err)
			}
		}

		// Tell run() goroutine that it can try to collect metrics.
		// If connection is lost, it will call us again.
		m.connectedChan <- true
	}
}

func (m *Monitor) GetShowStatusMetrics(conn *sql.DB, prefix string, c *mm.Collection) error {
		rows, err := conn.Query("SHOW /*!50002 GLOBAL */ STATUS")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var statName string
			var statValue string
			if err = rows.Scan(&statName, &statValue); err != nil {
				return err
			}

			if !m.stats[statName] {
				continue  // not collecting this stat
			}

			metricName := prefix + strings.ToLower(statName)
			metricValue, err := strconv.ParseFloat(statValue, 64)
			if err != nil {
				metricValue = 0.0
			}

			c.Metrics = append(c.Metrics, mm.Metric{metricName, metricValue})
		}
		err = rows.Err()
		if err != nil {
			return err
		}
		return nil
}

func (m *Monitor) GetInnoDBMetrics(conn *sql.DB, prefix string, c *mm.Collection) error {
	rows, err := conn.Query("SELECT NAME,SUBSYSTEM,COUNT,TYPE FROM INFORMATION_SCHEMA.INNODB_METRICS WHERE STATUS='enabled'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var statName string
		var statSubsystem string
		var statValue string
		var statType string
		err = rows.Scan(&statName, &statSubsystem, &statValue, &statType)
		if err != nil {
			return err
		}

		metricName := prefix + "/innodb_metrics/" + strings.ToLower(statSubsystem) + "/" + strings.ToLower(statName)
		metricValue, err := strconv.ParseFloat(statValue, 64)
		if err != nil {
			metricValue = 0.0
		}
		c.Metrics = append(c.Metrics, mm.Metric{metricName, metricValue})
	}
	err = rows.Err()
	if err != nil {
		return err
	}
	return nil
}

func (m *Monitor) GetTableStatMetrics(conn *sql.DB, prefix string, c *mm.Collection, ignoreDb string) error {
	/*
	   SELECT * FROM INFORMATION_SCHEMA.TABLE_STATISTICS;
	   +--------------+-------------+-----------+--------------+------------------------+
	   | TABLE_SCHEMA | TABLE_NAME  | ROWS_READ | ROWS_CHANGED | ROWS_CHANGED_X_INDEXES |
	*/
	tableStatSQL := "SELECT TABLE_SCHEMA,TABLE_NAME,ROWS_READ,ROWS_CHANGED,ROWS_CHANGED_X_INDEXES FROM INFORMATION_SCHEMA.TABLE_STATISTICS"
	if ignoreDb != "" {
		tableStatSQL = tableStatSQL + " WHERE TABLE_SCHEMA NOT LIKE '" + ignoreDb + "'"
	}
	rows, err := conn.Query(tableStatSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tableSchema string
		var tableName string
		var rowsRead int64
		var rowsChanged int64
		var rowsChangedIndexes int64
		err = rows.Scan(&tableSchema, &tableName, &rowsRead, &rowsChanged, &rowsChangedIndexes)
		if err != nil {
			return err
		}

		c.Metrics = append(c.Metrics, mm.Metric{
			Name: prefix+"db."+tableSchema+"/t."+tableName+"/rows_read",
			Value: float64(rowsRead),
		})
		c.Metrics = append(c.Metrics, mm.Metric{
			Name: prefix+"db."+tableSchema+"/t."+tableName+"/rows_changed",
			Value: float64(rowsChanged),
		})
		c.Metrics = append(c.Metrics, mm.Metric{
			Name: prefix+"db."+tableSchema+"/t."+tableName+"/rows_changed_x_indexes",
			Value: float64(rowsChangedIndexes),
		})
	}
	err = rows.Err()
	if err != nil {
		return err
	}
	return nil
}

func (m *Monitor) GetIndexStatMetrics(conn *sql.DB, prefix string, c *mm.Collection, ignoreDb string) error {
	/*
	   SELECT * FROM INFORMATION_SCHEMA.INDEX_STATISTICS;
	   +--------------+-------------+------------+-----------+
	   | TABLE_SCHEMA | TABLE_NAME  | INDEX_NAME | ROWS_READ | select * from INFORMATION_SCHEMA.INDEX_STATISTICS;
	   +--------------+-------------+------------+-----------+
	*/
	indexStatSQL := "SELECT TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,ROWS_READ FROM INFORMATION_SCHEMA.INDEX_STATISTICS"
	if ignoreDb != "" {
		indexStatSQL = indexStatSQL + " WHERE TABLE_SCHEMA NOT LIKE '" + ignoreDb + "'"
	}
	rows, err := conn.Query(indexStatSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tableSchema string
		var tableName string
		var indexName string
		var rowsRead int64
		err = rows.Scan(&tableSchema, &tableName, &indexName, &rowsRead)
		if err != nil {
			return err
		}

		metricName := prefix+"db."+tableSchema+"/t."+tableName+"/idx."+indexName+"/rows_read"
		metricValue := float64(rowsRead)
		c.Metrics = append(c.Metrics, mm.Metric{metricName, metricValue})
	}
	err = rows.Err()
	if err != nil {
		return err
	}
	return nil
}
