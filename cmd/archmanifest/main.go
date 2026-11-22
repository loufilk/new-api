// Command archmanifest produces a deterministic ownership snapshot for the
// modularization program from a Git diff and an ordered TSV rule set.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type rule struct {
	matchType   string
	pattern     string
	ownership   string
	module      string
	disposition string
	note        string
}

type diffRow struct {
	Path       string
	OldPath    string
	Status     string
	Insertions int
	Deletions  int
	Binary     bool
}

func main() {
	base := flag.String("base", "", "required base commit")
	head := flag.String("head", "HEAD", "head commit/ref to snapshot")
	rulesPath := flag.String("rules", "docs/architecture/modularization/ownership-rules.tsv", "ordered ownership rules")
	outPath := flag.String("out", "docs/architecture/modularization/fork-manifest.csv", "output CSV path, or - for stdout")
	flag.Parse()

	if strings.TrimSpace(*base) == "" {
		fatalf("-base is required")
	}

	rules, err := loadRules(*rulesPath)
	if err != nil {
		fatalf("load rules: %v", err)
	}
	rows, err := loadDiff(*base, *head)
	if err != nil {
		fatalf("load git diff: %v", err)
	}

	var out io.Writer = os.Stdout
	var file *os.File
	if *outPath != "-" {
		if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
			fatalf("create output dir: %v", err)
		}
		file, err = os.Create(*outPath)
		if err != nil {
			fatalf("create output: %v", err)
		}
		defer file.Close()
		out = file
	}

	w := csv.NewWriter(out)
	if err := w.Write([]string{"path", "old_path", "status", "insertions", "deletions", "binary", "ownership", "module", "artifact_kind", "disposition", "matched_rule", "rule_note"}); err != nil {
		fatalf("write header: %v", err)
	}
	counts := map[string]int{}
	for _, row := range rows {
		matched := classify(row.Path, rules)
		counts[matched.ownership]++
		if err := w.Write([]string{
			row.Path,
			row.OldPath,
			row.Status,
			strconv.Itoa(row.Insertions),
			strconv.Itoa(row.Deletions),
			strconv.FormatBool(row.Binary),
			matched.ownership,
			matched.module,
			artifactKind(row.Path),
			matched.disposition,
			matched.matchType + ":" + matched.pattern,
			matched.note,
		}); err != nil {
			fatalf("write row: %v", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fatalf("flush output: %v", err)
	}

	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(os.Stderr, "manifest rows=%d base=%s head=%s\n", len(rows), *base, *head)
	for _, key := range keys {
		fmt.Fprintf(os.Stderr, "%s=%d\n", key, counts[key])
	}
}

func loadRules(path string) ([]rule, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	var rules []rule
	line := 0
	for {
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		line++
		if len(record) == 0 || strings.HasPrefix(strings.TrimSpace(record[0]), "#") || record[0] == "match_type" {
			continue
		}
		if len(record) != 6 {
			return nil, fmt.Errorf("%s:%d: expected 6 TSV fields, got %d", path, line, len(record))
		}
		rules = append(rules, rule{matchType: record[0], pattern: record[1], ownership: record[2], module: record[3], disposition: record[4], note: record[5]})
	}
	return rules, nil
}

func loadDiff(base, head string) ([]diffRow, error) {
	nameStatus, err := git("diff", "--name-status", "--find-renames", base+"..."+head)
	if err != nil {
		return nil, err
	}
	numStat, err := git("diff", "--numstat", "--no-renames", base+"..."+head)
	if err != nil {
		return nil, err
	}

	stats := map[string]diffRow{}
	scanner := bufio.NewScanner(strings.NewReader(numStat))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 3 {
			continue
		}
		path := filepath.ToSlash(parts[2])
		row := diffRow{Path: path}
		if parts[0] == "-" || parts[1] == "-" {
			row.Binary = true
		} else {
			row.Insertions, _ = strconv.Atoi(parts[0])
			row.Deletions, _ = strconv.Atoi(parts[1])
		}
		stats[path] = row
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var rows []diffRow
	scanner = bufio.NewScanner(strings.NewReader(nameStatus))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := filepath.ToSlash(parts[len(parts)-1])
		row := stats[path]
		row.Path = path
		row.Status = status
		if (strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")) && len(parts) == 3 {
			row.OldPath = filepath.ToSlash(parts[1])
			// Numstat is intentionally collected with --no-renames for simple,
			// deterministic parsing. Renamed files therefore do not have a direct
			// new-path stat row; keep their per-file line counts at zero rather than
			// fabricate add/delete totals for a logical single-file rename.
			row.Insertions = 0
			row.Deletions = 0
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows, nil
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func classify(path string, rules []rule) rule {
	for _, candidate := range rules {
		if matches(path, candidate) {
			return candidate
		}
	}
	return rule{matchType: "default", pattern: "*", ownership: "UPSTREAM_CORE", module: "core-review", disposition: "REVIEW", note: "unclassified upstream-tree delta"}
}

func matches(path string, r rule) bool {
	switch r.matchType {
	case "exact":
		return path == r.pattern
	case "prefix":
		return strings.HasPrefix(path, r.pattern)
	case "suffix":
		return strings.HasSuffix(path, r.pattern)
	case "contains":
		return strings.Contains(path, r.pattern)
	default:
		return false
	}
}

func artifactKind(path string) string {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(path, "_test.go"), strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return "test"
	case strings.HasPrefix(path, "docs/") || strings.HasSuffix(path, ".md"):
		return "documentation"
	case strings.HasPrefix(path, "ops/") || strings.HasPrefix(path, ".github/"):
		return "ops"
	case strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "bin/") || strings.HasPrefix(path, "web/scripts/"):
		return "tooling"
	default:
		return "production"
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "archmanifest: "+format+"\n", args...)
	os.Exit(1)
}
