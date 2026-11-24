// Command lane-precustody-audit verifies a strict, quiesced PostgreSQL
// pre-custody topology. It never writes evidence to the database or grants
// authority. A sanitized receipt is emitted only after two fresh samples.
package main

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	auditModule  = "github.com/QuantumNous/new-api"
	auditDSNEnv  = "LANE_PRECUSTODY_AUDITOR_DSN"
	auditMaxFile = 64 << 10
)

var (
	errInventory = errors.New("invalid or unsupported inventory; exactly one direct shared MAIN+LOG primary is required")
	errSource    = errors.New("inventory, clean verifier source and candidate executable did not match")
	errDirect    = errors.New("direct verified PostgreSQL observer connection failed")
	errFresh     = errors.New("two fresh physical PostgreSQL samples did not agree")
	auditName    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	auditLabel   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
)

type inventoryV1 struct {
	Version           int          `json:"version"`
	Topology          string       `json:"topology"`
	InventorySHA256   string       `json:"operator_evidence_sha256"`
	SourceRevision    string       `json:"source_revision"`
	WritableEndpoints []endpointV1 `json:"writable_endpoints"`
}

type endpointV1 struct {
	Label string                    `json:"label"`
	Store model.LaneOperatorStoreV1 `json:"store"`
}

type receiptV1 struct {
	Version                  int                          `json:"version"`
	Result                   string                       `json:"result"`
	CustodyAuthorized        bool                         `json:"custody_authorized"`
	ExternalCoverageVerified bool                         `json:"external_coverage_verified"`
	SourceRevision           string                       `json:"source_revision"`
	CandidateBinarySHA256    string                       `json:"candidate_binary_sha256"`
	InputSHA256              string                       `json:"input_sha256"`
	OperatorEvidenceSHA256   string                       `json:"operator_evidence_sha256"`
	Endpoint                 string                       `json:"endpoint"`
	ServerVersionNum         int                          `json:"server_version_num"`
	Epoch                    int64                        `json:"epoch"`
	Samples                  []model.LaneOperatorSampleV1 `json:"samples"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lane-precustody-audit", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // flag errors may echo arbitrary user input
	path := flags.String("inventory", "", "non-secret inventory JSON path")
	candidate := flags.String("candidate", "", "offline New API executable path")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" || *candidate == "" {
		fmt.Fprintln(stderr, "usage: lane-precustody-audit -inventory FILE -candidate FILE")
		return 2
	}
	f, err := os.Open(*path)
	if err != nil {
		fmt.Fprintln(stderr, errInventory)
		return 1
	}
	raw, err := io.ReadAll(io.LimitReader(f, auditMaxFile+1))
	_ = f.Close()
	in, inputErr := decodeInventory(raw)
	if err != nil || inputErr != nil {
		fmt.Fprintln(stderr, errInventory)
		return 1
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || cleanRevision(info, auditModule+"/cmd/lane-precustody-audit") != in.SourceRevision ||
		!matchesCandidate(*candidate, in.SourceRevision, in.WritableEndpoints[0].Store.Transition.Release.BinarySHA256) || model.LaneStoreRuntimeCapable {
		fmt.Fprintln(stderr, errSource)
		return 1
	}
	// PostgreSQL environment defaults can silently redirect authentication or
	// supply credentials/options. The caller must use the one dedicated env.
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "PG") {
			fmt.Fprintln(stderr, errDirect)
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := audit(ctx, in, raw, os.Getenv(auditDSNEnv))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	out, err := common.Marshal(r)
	if err != nil {
		fmt.Fprintln(stderr, "receipt encoding failed")
		return 1
	}
	if _, err := fmt.Fprintln(stdout, string(out)); err != nil {
		fmt.Fprintln(stderr, "receipt output failed")
		return 1
	}
	return 0
}

func cleanRevision(info *debug.BuildInfo, path string) string {
	if info == nil || info.Path != path || info.Main.Path != auditModule {
		return ""
	}
	var revision string
	modified := true
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value != "false"
		}
	}
	if modified || !lowerHex(revision, 40) {
		return ""
	}
	return revision
}

func matchesCandidate(path, revision, digest string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return false
	}
	info, err := buildinfo.Read(f)
	if err != nil || cleanRevision(info, auditModule) != revision {
		return false
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	after, statErr := f.Stat()
	return err == nil && statErr == nil && n == before.Size() && n == after.Size() &&
		before.ModTime() == after.ModTime() && hex.EncodeToString(h.Sum(nil)) == digest
}

func lowerHex(s string, n int) bool {
	if len(s) != n || strings.ToLower(s) != s || strings.Trim(s, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func decodeInventory(raw []byte) (inventoryV1, error) {
	var in inventoryV1
	if len(raw) > auditMaxFile || common.DecodeJsonExactShape(raw, &in) != nil ||
		in.Version != 1 || in.Topology != "one_direct_shared_main_log_primary" || len(in.WritableEndpoints) != 1 ||
		!lowerHex(in.SourceRevision, 40) || !lowerHex(in.InventorySHA256, 64) {
		return inventoryV1{}, errInventory
	}
	e := in.WritableEndpoints[0]
	p := e.Store
	ip, ipErr := netip.ParseAddr(p.ServerAddr)
	id, idErr := strconv.ParseUint(p.SystemID, 10, 64)
	if !auditLabel.MatchString(e.Label) || ipErr != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.String() != p.ServerAddr || p.ServerPort < 1 || p.ServerPort > 65535 ||
		idErr != nil || id == 0 || p.SystemID != strconv.FormatUint(id, 10) || p.Timeline < 1 || p.Timeline > 1<<32-1 ||
		p.VersionNum != 180006 || p.MaxPrepared == nil || *p.MaxPrepared < 0 ||
		!auditName.MatchString(p.Database) || !auditName.MatchString(p.Schema) ||
		strings.HasPrefix(p.Schema, "pg_") || p.Schema == "information_schema" {
		return inventoryV1{}, errInventory
	}
	oids := map[int64]bool{}
	for _, oid := range []int64{p.DatabaseOID, p.SchemaOID, p.AuthorityOID, p.CatalogOID, p.MigrationOID} {
		if oid < 1 || oid > 1<<32-1 || oids[oid] {
			return inventoryV1{}, errInventory
		}
		oids[oid] = true
	}
	roleOIDs, roleNames := map[int64]bool{}, map[string]bool{}
	roles := []model.LaneOperatorRoleV1{p.Auditor, p.Owner}
	for _, set := range [][]model.LaneOperatorRoleV1{p.RetiredWriters, p.NextWriters, p.ControlAdmins} {
		if len(set) == 0 || len(set) > 32 {
			return inventoryV1{}, errInventory
		}
		roles = append(roles, set...)
	}
	for _, role := range roles {
		if !auditName.MatchString(role.Name) || strings.HasPrefix(role.Name, "pg_") ||
			role.OID < 1 || role.OID > 1<<32-1 || roleOIDs[role.OID] || roleNames[role.Name] {
			return inventoryV1{}, errInventory
		}
		roleOIDs[role.OID], roleNames[role.Name] = true, true
	}
	t := p.Transition
	if !lowerHex(t.Release.StoreID, 32) || !lowerHex(t.Release.ReleaseID, 32) || !lowerHex(t.Release.BinarySHA256, 64) ||
		t.Release.Epoch < 1 || !lowerHex(t.BaseReleaseID, 32) || !lowerHex(t.BaseBinarySHA256, 64) ||
		!strings.HasPrefix(t.MigrationRunID, t.BaseReleaseID+":") || len(t.MigrationRunID) <= 33 ||
		len(t.MigrationRunID) > 160 || strings.TrimSpace(t.MigrationRunID) != t.MigrationRunID {
		return inventoryV1{}, errInventory
	}
	return in, nil
}

func directConfig(dsn string, p model.LaneOperatorStoreV1) (*pgx.ConnConfig, error) {
	u, err := url.Parse(dsn)
	if err != nil || u == nil || u.Opaque != "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.User == nil || u.User.Username() != p.Auditor.Name || u.Path != "/"+p.Database ||
		u.Fragment != "" || u.Hostname() == "" || strings.Contains(u.Host, ",") || u.Port() != strconv.Itoa(p.ServerPort) {
		return nil, errDirect
	}
	password, present := u.User.Password()
	if !present || password == "" {
		return nil, errDirect
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["sslmode"]) != 1 || q.Get("sslmode") != "verify-full" ||
		len(q["sslrootcert"]) != 1 || q.Get("sslrootcert") == "" {
		return nil, errDirect
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg == nil || cfg.TLSConfig == nil || cfg.TLSConfig.InsecureSkipVerify || cfg.TLSConfig.ServerName != u.Hostname() ||
		len(cfg.Fallbacks) != 0 || cfg.Host != u.Hostname() || int(cfg.Port) != p.ServerPort ||
		cfg.Database != p.Database || cfg.User != p.Auditor.Name || cfg.Password != password {
		return nil, errDirect
	}
	pinned := net.JoinHostPort(p.ServerAddr, strconv.Itoa(p.ServerPort))
	cfg.LookupFunc = func(_ context.Context, _ string) ([]string, error) { return []string{p.ServerAddr}, nil }
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" || addr != pinned {
			return nil, errDirect
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", pinned)
	}
	cfg.RuntimeParams = map[string]string{
		"application_name":              "lane-precustody-audit-v1",
		"default_transaction_read_only": "on",
	}
	cfg.ConnectTimeout = 4 * time.Second
	return cfg, nil
}

func sample(ctx context.Context, cfg *pgx.ConnConfig, p model.LaneOperatorStoreV1) (model.LaneOperatorSampleV1, error) {
	pool := stdlib.OpenDB(*cfg)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(0)
	defer pool.Close()
	// Do not ping before the actual short transaction: that would create an
	// extra physical connection. The transaction validates TLS/session itself.
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{
		DisableAutomaticPing: true, Logger: logger.Discard,
	})
	if err != nil {
		return model.LaneOperatorSampleV1{}, errDirect
	}
	return model.CheckLaneOperatorSnapshotV1(ctx, db, p)
}

func audit(ctx context.Context, in inventoryV1, raw []byte, dsn string) (receiptV1, error) {
	e := in.WritableEndpoints[0]
	cfg, err := directConfig(dsn, e.Store)
	if err != nil {
		return receiptV1{}, err
	}
	samples := make([]model.LaneOperatorSampleV1, 0, 2)
	for range 2 {
		s, err := sample(ctx, cfg, e.Store)
		if err != nil {
			return receiptV1{}, err
		}
		samples = append(samples, s)
	}
	first, second := samples[0], samples[1]
	if first.BackendPID == second.BackendPID || !second.BackendStart.After(first.BackendStart) ||
		second.ObservedAt.Before(first.ObservedAt) || !first.Postmaster.Equal(second.Postmaster) {
		return receiptV1{}, errFresh
	}
	input := sha256.Sum256(raw)
	return receiptV1{
		Version: 1, Result: "observed_quiesced_no_custody_only", SourceRevision: in.SourceRevision,
		CandidateBinarySHA256: e.Store.Transition.Release.BinarySHA256, InputSHA256: hex.EncodeToString(input[:]),
		OperatorEvidenceSHA256: in.InventorySHA256, Endpoint: e.Label, ServerVersionNum: e.Store.VersionNum,
		Epoch: e.Store.Transition.Release.Epoch, Samples: samples,
	}, nil
}
