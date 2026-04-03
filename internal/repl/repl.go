package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/duckstream/duckstream/internal/query"
)

type REPL struct {
	ctx     context.Context
	manager *query.Manager
	scanner *bufio.Scanner
}

func NewREPL(ctx context.Context, manager *query.Manager) *REPL {
	return &REPL{
		ctx:     ctx,
		manager: manager,
		scanner: bufio.NewScanner(os.Stdin),
	}
}

var (
	// Supported syntax:
	// REGISTER QUERY <id> AS <sql>
	// REGISTER QUERY <id> AS <sql> WITH CURSOR <column>
	// REGISTER QUERY <id> AS <sql> FROM NOW
	// REGISTER QUERY <id> AS <sql> WITH CURSOR <column> FROM NOW
	registerRe   = regexp.MustCompile(`(?i)^REGISTER QUERY\s+(\w+)\s+AS\s+(.+?)(?:\s+WITH\s+CURSOR\s+(\w+))?(?:\s+FROM\s+NOW)?\s*$`)
	unregisterRe = regexp.MustCompile(`(?i)^UNREGISTER QUERY\s+(\w+)$`)
	listRe       = regexp.MustCompile(`(?i)^LIST QUERIES$`)
	helpRe       = regexp.MustCompile(`(?i)^HELP$`)
	quitRe       = regexp.MustCompile(`(?i)^QUIT$`)
)

func (r *REPL) Run() error {
	fmt.Println("DuckStream Control Surface")
	fmt.Println("Commands:")
	fmt.Println("  REGISTER QUERY <id> AS <sql>")
	fmt.Println("  UNREGISTER QUERY <id>")
	fmt.Println("  LIST QUERIES")
	fmt.Println("  HELP")
	fmt.Println("  QUIT")
	fmt.Println()

	for {
		fmt.Print("> ")
		if !r.scanner.Scan() {
			break
		}

		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}

		if err := r.process(line); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}

	return nil
}

func (r *REPL) process(line string) error {
	if quitRe.MatchString(line) {
		fmt.Println("Goodbye!")
		os.Exit(0)
	}

	if helpRe.MatchString(line) {
		fmt.Println("Commands:")
		fmt.Println("  REGISTER QUERY <id> AS <sql>  - Register a streaming query")
		fmt.Println("  UNREGISTER QUERY <id>         - Stop and remove a query")
		fmt.Println("  LIST QUERIES                  - Show active queries")
		fmt.Println("  HELP                          - Show this help")
		fmt.Println("  QUIT                          - Exit the REPL")
		return nil
	}

	if id, sql, cursorHint, mode, ok := parseRegisterCommand(line); ok {
		return r.manager.Register(r.ctx, id, sql, cursorHint, mode)
	}

	if matches := unregisterRe.FindStringSubmatch(line); matches != nil {
		id := matches[1]
		return r.manager.Unregister(r.ctx, id)
	}

	if listRe.MatchString(line) {
		queries := r.manager.List()
		if len(queries) == 0 {
			fmt.Println("No active queries")
			return nil
		}
		fmt.Println("Active queries:")
		for _, q := range queries {
			fmt.Printf("  %s: %s (active: %v)\n", q.ID, q.SQL, q.Active)
		}
		return nil
	}

	return fmt.Errorf("unknown command: %s", line)
}

func parseRegisterCommand(line string) (id, sql string, cursorHint *string, mode query.StartMode, ok bool) {
	matches := registerRe.FindStringSubmatch(line)
	if matches == nil {
		return "", "", nil, query.StartModeBeginning, false
	}

	id = matches[1]
	sql = strings.TrimSpace(matches[2])
	mode = query.StartModeBeginning
	cursorHint = nil

	if matches[3] != "" {
		cursorHint = &matches[3]
	}

	if strings.Contains(strings.ToUpper(line), "FROM NOW") {
		mode = query.StartModeNow
	}

	return id, sql, cursorHint, mode, true
}
