// Package agentverify checks the fictional plan using the controller's rules.
// It does not execute the plan or load rules from learner-controlled files.
package agentverify

import (
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Generated copy of passport_system/agent_contract.json. Keep synchronized
// with scripts/sync_agent_contract.py; the source suite checks byte equality.
//
//go:embed contract.json
var contractJSON []byte

type rule struct {
	ID   string     `json:"id"`
	All  [][]string `json:"all"`
	Deny []string   `json:"deny"`
}

var contract struct {
	Artifact           string `json:"artifact"`
	MaxBytes           int64  `json:"max_bytes_exclusive"`
	StatementSeparator string `json:"statement_separator"`
	PolicyLineStart    string `json:"policy_line_start"`
	NegatedClaimPrefix string `json:"negated_claim_prefix"`
	Rules              []rule `json:"rules"`
}

type compiledRule struct {
	id     string
	groups [][]*regexp.Regexp
	deny   []*regexp.Regexp
}

var rules []compiledRule
var separator *regexp.Regexp
var policyLine *regexp.Regexp
var negatedPrefix *regexp.Regexp
var whitespace = regexp.MustCompile(`[ \t\n\v\f\r]+`)

func init() {
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		panic(err)
	}
	separator = regexp.MustCompile(contract.StatementSeparator)
	policyLine = regexp.MustCompile(contract.PolicyLineStart)
	negatedPrefix = regexp.MustCompile(contract.NegatedClaimPrefix)
	for _, entry := range contract.Rules {
		compiled := compiledRule{id: entry.ID}
		for _, group := range entry.All {
			patterns := []*regexp.Regexp{}
			for _, pattern := range group {
				patterns = append(patterns, regexp.MustCompile(pattern))
			}
			compiled.groups = append(compiled.groups, patterns)
		}
		for _, pattern := range entry.Deny {
			compiled.deny = append(compiled.deny, regexp.MustCompile(pattern))
		}
		rules = append(rules, compiled)
	}
}

func failedChecks() map[string]bool {
	checks := map[string]bool{"bounded_file": false}
	for _, entry := range rules {
		checks[entry.id] = false
	}
	return checks
}

func matchesAny(patterns []*regexp.Regexp, text string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// ContentChecks recognizes explicit rules in sentences/list items, with line
// wrapping and Markdown emphasis ignored. Word order and distance are free.
func ContentChecks(content []byte) map[string]bool {
	checks := failedChecks()
	if int64(len(content)) >= contract.MaxBytes || !utf8.Valid(content) || strings.ContainsRune(string(content), 0) {
		return checks
	}
	checks["bounded_file"] = true
	text := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, string(content))
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	formatting := strings.NewReplacer("`", "", "*", "", "_", "")
	text = formatting.Replace(separator.ReplaceAllString(text, ";"))
	text = policyLine.ReplaceAllString(text, ";$0")
	statements := strings.Split(text, ";")
	for i, part := range statements {
		statements[i] = strings.Trim(whitespace.ReplaceAllString(part, " "), " ")
	}
	for _, entry := range rules {
		found, denied := false, false
		for _, part := range statements {
			matched := true
			for _, group := range entry.groups {
				matched = matched && matchesAny(group, part)
			}
			found = found || matched
			for _, pattern := range entry.deny {
				for _, match := range pattern.FindAllStringIndex(part, -1) {
					denied = denied || !negatedPrefix.MatchString(part[:match[0]])
				}
			}
		}
		checks[entry.id] = found && !denied
	}
	return checks
}

// CheckPlan refuses symlinked exercise paths and reads at most the size limit.
func CheckPlan(root string) map[string]bool {
	path := root
	parts := strings.Split(contract.Artifact, "/")
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return failedChecks()
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return failedChecks()
			}
		} else if !info.Mode().IsRegular() || info.Size() >= contract.MaxBytes {
			return failedChecks()
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return failedChecks()
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, contract.MaxBytes))
	if err != nil {
		return failedChecks()
	}
	return ContentChecks(content)
}
