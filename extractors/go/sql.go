package main

import (
	"regexp"
	"strings"
)

// Table is one CREATE TABLE parsed from a schema file.
type Table struct {
	Name    string
	Columns []string // "name:type"
	Line    int      // 1-based line of the CREATE TABLE keyword
}

var createTableRe = regexp.MustCompile(`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?` +
	"[`\"']?([a-zA-Z_][a-zA-Z0-9_.]*)[`\"']?\\s*\\(")

// nonColumnStart matches lines that declare a constraint, not a column.
var nonColumnStart = regexp.MustCompile(`(?i)^(primary|foreign|unique|constraint|check|key|index|exclude)\b`)

// ParseTables extracts CREATE TABLE statements from one SQL source.
func ParseTables(sql string) []Table {
	var tables []Table
	for _, loc := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
		name := sql[loc[2]:loc[3]]
		body, ok := balancedBody(sql, loc[1]-1) // loc[1]-1 points at "("
		if !ok {
			continue
		}
		tables = append(tables, Table{
			Name:    strings.Trim(name, "`\"'"),
			Columns: parseColumns(body),
			Line:    1 + strings.Count(sql[:loc[0]], "\n"),
		})
	}
	return tables
}

// balancedBody returns the text inside the parenthesis group starting at
// openIdx (which must index a '('), excluding the outer parentheses.
func balancedBody(s string, openIdx int) (string, bool) {
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[openIdx+1 : i], true
			}
		}
	}
	return "", false
}

// parseColumns splits a CREATE TABLE body into "name:type" column entries,
// skipping table-level constraints and respecting nested parentheses.
func parseColumns(body string) []string {
	var columns []string
	depth := 0
	start := 0
	flush := func(end int) {
		field := strings.TrimSpace(body[start:end])
		start = end + 1
		if field == "" || nonColumnStart.MatchString(field) {
			return
		}
		fields := strings.Fields(field)
		if len(fields) == 0 {
			return
		}
		colName := strings.Trim(fields[0], "`\"'")
		colType := ""
		if len(fields) > 1 {
			colType = strings.ToLower(strings.Trim(fields[1], "`\"',"))
		}
		if colType == "" {
			columns = append(columns, colName)
		} else {
			columns = append(columns, colName+":"+colType)
		}
	}
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
			}
		}
	}
	flush(len(body))
	return columns
}
