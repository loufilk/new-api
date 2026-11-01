// Command no-agent-fixture exercises production schema composition on an owned
// disposable PostgreSQL fixture. It never starts HTTP or background workers.
package main

import (
	"database/sql"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
)

func main() {
	if os.Getenv("NO_AGENT_FIXTURE_OWNED") != "1" {
		panic("requires an owned disposable cluster")
	}
	u, err := url.Parse(os.Getenv("SQL_DSN"))
	if err != nil || !net.ParseIP(u.Hostname()).IsLoopback() || !strings.HasPrefix(u.Path, "/no_agent_") {
		panic("fixture must use a loopback no_agent_ database")
	}
	probe, err := sql.Open("pgx", os.Getenv("SQL_DSN"))
	if err != nil {
		panic(err)
	}
	var version int
	err = probe.QueryRow("SHOW server_version_num").Scan(&version)
	_ = probe.Close()
	if err != nil || version != 180006 {
		panic("fixture requires PostgreSQL 18.6 before any migration")
	}
	common.IsMasterNode, common.RedisEnabled = true, false
	reg := extension.NewRegistry()
	if err := chuanapi.Register(reg); err != nil {
		panic(err)
	}
	if err := extension.SetDefault(reg); err != nil {
		panic(err)
	}
	if err := model.InitDB(); err != nil {
		panic(err)
	}
	defer model.CloseDB()
	if err := model.InitLogDB(); err != nil {
		panic(err)
	}
	fmt.Println("production migration completed on PostgreSQL 18.6")
}
