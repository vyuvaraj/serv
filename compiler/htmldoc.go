package compiler

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// DocComment holds triple-slash documentation comment details.
type DocComment struct {
	Description string
	Params      map[string]string // param name -> description
	Returns     string
}

// GenerateHTMLDocs generates an interactive HTML documentation page from the AST program.
// It also reads the file source code to parse triple-slash (///) comments.
func GenerateHTMLDocs(prog *Program, entryFile string) (string, error) {
	// Parse triple-slash comments from the entry file and any imported files
	comments := parseDocComments(entryFile)

	// Collect info
	var routes []*RouteStmt
	var crons []*CronStmt
	var everys []*EveryStmt
	var dbs []*DatabaseStmt
	var brokers []*BrokerStmt
	var wss []*WsStmt
	var subscribes []*SubscribeStmt

	var collect func(statements []Statement)
	collect = func(statements []Statement) {
		for _, stmt := range statements {
			switch s := stmt.(type) {
			case *RouteStmt:
				routes = append(routes, s)
			case *CronStmt:
				crons = append(crons, s)
			case *EveryStmt:
				everys = append(everys, s)
			case *DatabaseStmt:
				dbs = append(dbs, s)
			case *BrokerStmt:
				brokers = append(brokers, s)
			case *WsStmt:
				wss = append(wss, s)
			case *SubscribeStmt:
				subscribes = append(subscribes, s)
			case *ExportStmt:
				switch inner := s.Inner.(type) {
				case *RouteStmt:
					routes = append(routes, inner)
				case *CronStmt:
					crons = append(crons, inner)
				case *EveryStmt:
					everys = append(everys, inner)
				case *DatabaseStmt:
					dbs = append(dbs, inner)
				case *BrokerStmt:
					brokers = append(brokers, inner)
				case *WsStmt:
					wss = append(wss, inner)
				case *SubscribeStmt:
					subscribes = append(subscribes, inner)
				}
			}
		}
	}
	collect(prog.Statements)

	var sb strings.Builder

	// Write Premium HTML page
	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Servverse API & Service Documentation</title>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-primary: #0a0e17;
            --bg-secondary: #131924;
            --bg-tertiary: #1b2332;
            --accent-primary: #6366f1;
            --accent-secondary: #a855f7;
            --text-primary: #f8fafc;
            --text-secondary: #94a3b8;
            --border-color: #2e3b52;
            --badge-get: #10b981;
            --badge-post: #6366f1;
            --badge-put: #f59e0b;
            --badge-delete: #ef4444;
            --badge-ws: #06b6d4;
            --badge-cron: #ec4899;
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: 'Outfit', sans-serif;
            background-color: var(--bg-primary);
            color: var(--text-primary);
            display: flex;
            min-height: 100vh;
            line-height: 1.5;
        }

        /* Sidebar Navigation */
        aside {
            width: 320px;
            background-color: var(--bg-secondary);
            border-right: 1px solid var(--border-color);
            padding: 2.5rem 1.5rem;
            position: sticky;
            top: 0;
            height: 100vh;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
            gap: 2rem;
        }

        .logo {
            font-size: 1.75rem;
            font-weight: 800;
            background: linear-gradient(135deg, var(--accent-primary), var(--accent-secondary));
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            letter-spacing: -0.5px;
            display: flex;
            align-items: center;
            gap: 0.5rem;
        }

        .logo span {
            font-size: 0.85rem;
            font-weight: 400;
            background: rgba(99, 102, 241, 0.15);
            border: 1px solid rgba(99, 102, 241, 0.3);
            color: var(--accent-primary);
            padding: 2px 8px;
            border-radius: 9999px;
            -webkit-text-fill-color: var(--accent-primary);
        }

        .nav-section-title {
            font-size: 0.75rem;
            text-transform: uppercase;
            letter-spacing: 1.5px;
            color: var(--text-secondary);
            margin-bottom: 0.75rem;
            font-weight: 600;
        }

        .nav-list {
            list-style: none;
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
        }

        .nav-link {
            display: flex;
            align-items: center;
            gap: 0.75rem;
            color: var(--text-secondary);
            text-decoration: none;
            padding: 0.6rem 0.75rem;
            border-radius: 8px;
            font-weight: 500;
            transition: all 0.2s ease;
        }

        .nav-link:hover {
            color: var(--text-primary);
            background-color: var(--bg-tertiary);
            transform: translateX(4px);
        }

        .nav-link.active {
            color: var(--text-primary);
            background-color: rgba(99, 102, 241, 0.15);
            border-left: 3px solid var(--accent-primary);
        }

        /* Main Content Container */
        main {
            flex-grow: 1;
            padding: 4rem 5% 5rem 5%;
            overflow-y: auto;
            max-width: 1200px;
        }

        .doc-section {
            margin-bottom: 5rem;
            scroll-margin-top: 4rem;
        }

        .section-header {
            font-size: 2.25rem;
            font-weight: 800;
            margin-bottom: 2rem;
            letter-spacing: -0.5px;
            border-bottom: 2px solid var(--border-color);
            padding-bottom: 0.75rem;
        }

        /* Documentation Cards */
        .card {
            background-color: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 12px;
            padding: 2rem;
            margin-bottom: 2rem;
            transition: all 0.3s ease;
            position: relative;
            overflow: hidden;
        }

        .card::before {
            content: '';
            position: absolute;
            top: 0;
            left: 0;
            width: 4px;
            height: 100%;
            background: transparent;
            transition: background-color 0.2s ease;
        }

        .card:hover {
            transform: translateY(-2px);
            border-color: rgba(99, 102, 241, 0.4);
            box-shadow: 0 10px 30px -10px rgba(0, 0, 0, 0.5);
        }

        /* Method and Badge styling */
        .badge {
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.8rem;
            font-weight: 700;
            padding: 4px 10px;
            border-radius: 6px;
            text-transform: uppercase;
            display: inline-block;
            margin-right: 1rem;
        }

        .badge-GET { background-color: rgba(16, 185, 129, 0.15); color: var(--badge-get); border: 1px solid rgba(16, 185, 129, 0.3); }
        .badge-POST { background-color: rgba(99, 102, 241, 0.15); color: var(--badge-post); border: 1px solid rgba(99, 102, 241, 0.3); }
        .badge-PUT { background-color: rgba(245, 158, 11, 0.15); color: var(--badge-put); border: 1px solid rgba(245, 158, 11, 0.3); }
        .badge-DELETE { background-color: rgba(239, 68, 68, 0.15); color: var(--badge-delete); border: 1px solid rgba(239, 68, 68, 0.3); }
        .badge-WS { background-color: rgba(6, 182, 212, 0.15); color: var(--badge-ws); border: 1px solid rgba(6, 182, 212, 0.3); }
        .badge-CRON { background-color: rgba(236, 72, 153, 0.15); color: var(--badge-cron); border: 1px solid rgba(236, 72, 153, 0.3); }

        .card-header-row {
            display: flex;
            align-items: center;
            margin-bottom: 1.25rem;
        }

        .card-title {
            font-size: 1.5rem;
            font-weight: 600;
            font-family: 'JetBrains Mono', monospace;
            color: var(--text-primary);
        }

        .card-desc {
            color: var(--text-secondary);
            font-size: 1rem;
            margin-bottom: 1.5rem;
        }

        /* Tables & Lists inside Cards */
        .info-table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 1.5rem;
            font-size: 0.95rem;
        }

        .info-table th, .info-table td {
            text-align: left;
            padding: 0.75rem 1rem;
            border-bottom: 1px solid var(--border-color);
        }

        .info-table th {
            font-weight: 600;
            color: var(--text-primary);
            background-color: var(--bg-tertiary);
        }

        .info-table td {
            color: var(--text-secondary);
        }

        .code-style {
            font-family: 'JetBrains Mono', monospace;
            background-color: var(--bg-tertiary);
            padding: 2px 6px;
            border-radius: 4px;
            color: var(--text-primary);
            font-size: 0.85rem;
        }

        .empty-state {
            color: var(--text-secondary);
            font-style: italic;
            padding: 1rem 0;
        }

        /* Glassmorphic header banner */
        .banner {
            background: linear-gradient(135deg, rgba(99, 102, 241, 0.1), rgba(168, 85, 247, 0.1));
            border: 1px solid var(--border-color);
            padding: 2.5rem;
            border-radius: 16px;
            margin-bottom: 4rem;
            backdrop-filter: blur(10px);
        }

        .banner h1 {
            font-size: 2.5rem;
            font-weight: 800;
            margin-bottom: 0.5rem;
            letter-spacing: -1px;
        }

        .banner p {
            color: var(--text-secondary);
            font-size: 1.1rem;
        }
    </style>
</head>
<body>
    <aside>
        <div class="logo">Servverse <span>docs</span></div>
        <div>
            <div class="nav-section-title">Navigation</div>
            <nav class="nav-list">
                <a href="#overview" class="nav-link">Overview</a>
                <a href="#routes" class="nav-link">HTTP Routes</a>
                <a href="#websockets" class="nav-link">WebSockets</a>
                <a href="#subscribes" class="nav-link">Subscriptions</a>
                <a href="#cronjobs" class="nav-link">Scheduled Jobs</a>
                <a href="#infras" class="nav-link">Infrastructure</a>
            </nav>
        </div>
    </aside>

    <main>
        <div class="banner" id="overview">
            <h1>Ecosystem Interactive API Documentation</h1>
            <p>Autogenerated directly from Serv-lang source code AST & docstrings.</p>
        </div>
`)

	// 1. HTTP Routes Section
	sb.WriteString(`
        <section class="doc-section" id="routes">
            <h2 class="section-header">HTTP Routes</h2>
    `)

	if len(routes) == 0 {
		sb.WriteString(`<div class="empty-state">No HTTP routes declared in this service.</div>`)
	} else {
		for _, r := range routes {
			method := strings.ToUpper(r.Method)
			descKey := fmt.Sprintf("route:%s:%s", method, r.Path)
			commentInfo := comments[descKey]
			if commentInfo.Description == "" {
				commentInfo.Description = "No description provided."
			}

			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge badge-%s">%s</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">%s</div>
            `, method, method, r.Path, commentInfo.Description))

			// Write middlewares if any
			if len(r.Middlewares) > 0 {
				sb.WriteString(`<div style="margin-bottom: 1rem;"><strong style="font-size: 0.9rem;">Middleware applied:</strong> `)
				for _, mw := range r.Middlewares {
					sb.WriteString(fmt.Sprintf(`<span class="code-style" style="margin-right: 0.5rem;">%s</span>`, mw))
				}
				sb.WriteString(`</div>`)
			}

			// Write rate limits if any
			if r.LimitRate > 0 {
				sb.WriteString(fmt.Sprintf(`<div style="margin-bottom: 1.5rem;"><strong style="font-size: 0.9rem; color: var(--badge-delete);">Rate Limit:</strong> %d requests per <span class="code-style">%s</span></div>`, r.LimitRate, r.LimitPeriod))
			}

			// Write request validation block if present
			rules, keys := parseValidationMap(r.Body)
			if len(rules) > 0 {
				sb.WriteString(`
                <strong style="font-size: 0.95rem; display: block; margin-top: 1rem;">Request Validation Schema (JSON payload)</strong>
                <table class="info-table">
                    <thead>
                        <tr>
                            <th>Field</th>
                            <th>Validation Rule</th>
                            <th>Description</th>
                        </tr>
                    </thead>
                    <tbody>
                `)
				for _, key := range keys {
					rule := rules[key]
					fDesc := commentInfo.Params[key]
					if fDesc == "" {
						fDesc = "No field description."
					}
					sb.WriteString(fmt.Sprintf(`
                        <tr>
                            <td><span class="code-style">%s</span></td>
                            <td><span class="code-style" style="color: var(--accent-secondary);">%s</span></td>
                            <td>%s</td>
                        </tr>
                    `, key, rule, fDesc))
				}
				sb.WriteString(`
                    </tbody>
                </table>
                `)
			}

			sb.WriteString(`</div>`)
		}
	}
	sb.WriteString(`</section>`)

	// 2. WebSockets Section
	sb.WriteString(`
        <section class="doc-section" id="websockets">
            <h2 class="section-header">WebSockets Channels</h2>
    `)

	if len(wss) == 0 {
		sb.WriteString(`<div class="empty-state">No WebSocket endpoints declared in this service.</div>`)
	} else {
		for _, ws := range wss {
			descKey := fmt.Sprintf("ws:%s", ws.Path)
			commentInfo := comments[descKey]
			if commentInfo.Description == "" {
				commentInfo.Description = "No description provided."
			}
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge badge-WS">WS</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">%s</div>
                <div style="font-size: 0.95rem; color: var(--text-secondary);">
                    Connection parameter handle: <span class="code-style">%s</span>
                </div>
            </div>
            `, ws.Path, commentInfo.Description, ws.Param))
		}
	}
	sb.WriteString(`</section>`)

	// 3. Subscriptions Section
	sb.WriteString(`
        <section class="doc-section" id="subscribes">
            <h2 class="section-header">Event Broker Subscriptions</h2>
    `)

	if len(subscribes) == 0 {
		sb.WriteString(`<div class="empty-state">No event broker subscriptions declared in this service.</div>`)
	} else {
		for _, sub := range subscribes {
			topicStr := sub.Topic.String()
			// Strip quotes from literal representation
			topicStr = strings.Trim(topicStr, `"`+"`")
			descKey := fmt.Sprintf("subscribe:%s", topicStr)
			commentInfo := comments[descKey]
			if commentInfo.Description == "" {
				commentInfo.Description = "No description provided."
			}
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge" style="background-color: rgba(99, 102, 241, 0.15); color: var(--accent-primary); border: 1px solid rgba(99, 102, 241, 0.3);">TOPIC</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">%s</div>
                <div style="font-size: 0.95rem; color: var(--text-secondary);">
                    Payload handler parameter: <span class="code-style">%s</span>
                </div>
            </div>
            `, topicStr, commentInfo.Description, sub.Param))
		}
	}
	sb.WriteString(`</section>`)

	// 4. Scheduled Jobs (Cron & Every)
	sb.WriteString(`
        <section class="doc-section" id="cronjobs">
            <h2 class="section-header">Scheduled Background Jobs</h2>
    `)

	if len(crons) == 0 && len(everys) == 0 {
		sb.WriteString(`<div class="empty-state">No background cron or interval jobs declared in this service.</div>`)
	} else {
		for _, c := range crons {
			cronExpr := strings.Trim(c.Cron.String(), `"`+"`")
			descKey := fmt.Sprintf("cron:%s", cronExpr)
			commentInfo := comments[descKey]
			if commentInfo.Description == "" {
				commentInfo.Description = "No description provided."
			}
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge badge-CRON">CRON</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">%s</div>
            </div>
            `, cronExpr, commentInfo.Description))
		}

		for _, e := range everys {
			intervalExpr := strings.Trim(e.Interval.String(), `"`+"`")
			descKey := fmt.Sprintf("every:%s", intervalExpr)
			commentInfo := comments[descKey]
			if commentInfo.Description == "" {
				commentInfo.Description = "No description provided."
			}
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge" style="background-color: rgba(168, 85, 247, 0.15); color: var(--accent-secondary); border: 1px solid rgba(168, 85, 247, 0.3);">EVERY</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">%s</div>
            </div>
            `, intervalExpr, commentInfo.Description))
		}
	}
	sb.WriteString(`</section>`)

	// 5. Infrastructure Section
	sb.WriteString(`
        <section class="doc-section" id="infras">
            <h2 class="section-header">Infrastructure Connections</h2>
    `)

	if len(dbs) == 0 && len(brokers) == 0 {
		sb.WriteString(`<div class="empty-state">No external database or message broker declared in this service.</div>`)
	} else {
		for _, db := range dbs {
			dbStr := strings.Trim(db.Value.String(), `"`+"`")
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge" style="background-color: rgba(16, 185, 129, 0.15); color: var(--badge-get); border: 1px solid rgba(16, 185, 129, 0.3);">DATABASE</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">Active relational database connection. Schemas & queries run against this backend source.</div>
            </div>
            `, dbStr))
		}

		for _, br := range brokers {
			brokerStr := strings.Trim(br.Value.String(), `"`+"`")
			sb.WriteString(fmt.Sprintf(`
            <div class="card">
                <div class="card-header-row">
                    <span class="badge" style="background-color: rgba(99, 102, 241, 0.15); color: var(--accent-primary); border: 1px solid rgba(99, 102, 241, 0.3);">BROKER</span>
                    <span class="card-title">%s</span>
                </div>
                <div class="card-desc">Active Pub/Sub event broker connections. Message queues bind here.</div>
            </div>
            `, brokerStr))
		}
	}
	sb.WriteString(`</section>`)

	sb.WriteString(`
    </main>
</body>
</html>
`)

	return sb.String(), nil
}

// parseDocComments parses a file and retrieves triple-slash comments (///) associated with services/routes.
func parseDocComments(filePath string) map[string]DocComment {
	results := make(map[string]DocComment)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return results
	}

	lines := strings.Split(string(content), "\n")
	var activeComment []string

	// Match statements to attach comments
	routeReg := regexp.MustCompile(`(?:export\s+)?route\s+"([^"]+)"\s+"([^"]+)"`)
	wsReg := regexp.MustCompile(`ws\s+"([^"]+)"`)
	subscribeReg := regexp.MustCompile(`subscribe\s+"([^"]+)"`)
	cronReg := regexp.MustCompile(`cron\s+"([^"]+)"`)
	everyReg := regexp.MustCompile(`every\s+"([^"]+)"`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "///") {
			commentContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "///"))
			activeComment = append(activeComment, commentContent)
			continue
		}

		if len(activeComment) > 0 {
			// Check if this line is a declaration
			var key string
			if match := routeReg.FindStringSubmatch(trimmed); len(match) > 0 {
				key = fmt.Sprintf("route:%s:%s", strings.ToUpper(match[1]), match[2])
			} else if match := wsReg.FindStringSubmatch(trimmed); len(match) > 0 {
				key = fmt.Sprintf("ws:%s", match[1])
			} else if match := subscribeReg.FindStringSubmatch(trimmed); len(match) > 0 {
				key = fmt.Sprintf("subscribe:%s", match[1])
			} else if match := cronReg.FindStringSubmatch(trimmed); len(match) > 0 {
				key = fmt.Sprintf("cron:%s", match[1])
			} else if match := everyReg.FindStringSubmatch(trimmed); len(match) > 0 {
				key = fmt.Sprintf("every:%s", match[1])
			}

			if key != "" {
				// Parse comment content
				var descLines []string
				params := make(map[string]string)
				var returns string

				for _, cLine := range activeComment {
					if strings.HasPrefix(cLine, "@param") {
						parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(cLine, "@param")), " ", 2)
						if len(parts) == 2 {
							params[parts[0]] = parts[1]
						}
					} else if strings.HasPrefix(cLine, "@returns") {
						returns = strings.TrimSpace(strings.TrimPrefix(cLine, "@returns"))
					} else {
						descLines = append(descLines, cLine)
					}
				}

				results[key] = DocComment{
					Description: strings.Join(descLines, " "),
					Params:      params,
					Returns:     returns,
				}
			}
			activeComment = nil
		}
	}

	return results
}
