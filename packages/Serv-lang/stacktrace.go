package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"sync"

	"serv/dap"
)

var stackLineRegex = regexp.MustCompile(`^(\s+)(.*?(?:main|service|serv_test)\.go):(\d+)(.*)$`)

type StackTraceRewriter struct {
	srvFile string
	smCache map[string]*dap.SourceMap
	mu      sync.Mutex
}

func NewStackTraceRewriter(srvFile string) *StackTraceRewriter {
	return &StackTraceRewriter{
		srvFile: srvFile,
		smCache: make(map[string]*dap.SourceMap),
	}
}

func (r *StackTraceRewriter) getSourceMap(goFile string) *dap.SourceMap {
	r.mu.Lock()
	defer r.mu.Unlock()

	if sm, exists := r.smCache[goFile]; exists {
		return sm
	}

	sm, err := dap.ParseSourceMap(goFile, r.srvFile)
	if err != nil {
		r.smCache[goFile] = nil
		return nil
	}
	r.smCache[goFile] = sm
	return sm
}

func (r *StackTraceRewriter) Rewrite(src io.Reader, dst io.Writer) {
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := scanner.Text()
		matches := stackLineRegex.FindStringSubmatch(line)
		if len(matches) > 0 {
			indent := matches[1]
			goFile := matches[2]
			lineStr := matches[3]
			suffix := matches[4]

			goLine, err := strconv.Atoi(lineStr)
			if err == nil {
				sm := r.getSourceMap(goFile)
				if sm != nil {
					srvLine, ok := sm.GoToSrvApprox(goLine)
					if ok {
						line = fmt.Sprintf("%s%s:%d%s", indent, r.srvFile, srvLine, suffix)
					}
				}
			}
		}
		fmt.Fprintln(dst, line)
	}
	if err := scanner.Err(); err != nil {
		// Handled scanner error
	}
}
