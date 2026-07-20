package client

const inspectorHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ServTunnel Inspector</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&family=Fira+Code:wght@400;500&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-color: #0b0f19;
            --panel-bg: rgba(20, 27, 45, 0.6);
            --panel-border: rgba(255, 255, 255, 0.08);
            --text-primary: #f3f4f6;
            --text-secondary: #9ca3af;
            --text-muted: #6b7280;
            --primary: #6366f1;
            --primary-hover: #4f46e5;
            
            --badge-get-bg: rgba(59, 130, 246, 0.15);
            --badge-get-text: #60a5fa;
            --badge-post-bg: rgba(16, 185, 129, 0.15);
            --badge-post-text: #34d399;
            --badge-put-bg: rgba(245, 158, 11, 0.15);
            --badge-put-text: #fbbf24;
            --badge-delete-bg: rgba(239, 68, 68, 0.15);
            --badge-delete-text: #f87171;
            --badge-other-bg: rgba(107, 114, 128, 0.15);
            --badge-other-text: #9ca3af;

            --status-2xx-bg: rgba(16, 185, 129, 0.15);
            --status-2xx-text: #34d399;
            --status-2xx-border: rgba(16, 185, 129, 0.3);
            --status-3xx-bg: rgba(59, 130, 246, 0.15);
            --status-3xx-text: #60a5fa;
            --status-3xx-border: rgba(59, 130, 246, 0.3);
            --status-4xx-bg: rgba(245, 158, 11, 0.15);
            --status-4xx-text: #fbbf24;
            --status-4xx-border: rgba(245, 158, 11, 0.3);
            --status-5xx-bg: rgba(239, 68, 68, 0.15);
            --status-5xx-text: #f87171;
            --status-5xx-border: rgba(239, 68, 68, 0.3);
            --status-pending-bg: rgba(107, 114, 128, 0.15);
            --status-pending-text: #9ca3af;
            --status-pending-border: rgba(107, 114, 128, 0.3);
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: 'Inter', -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background-color: var(--bg-color);
            color: var(--text-primary);
            height: 100vh;
            overflow: hidden;
            display: flex;
            flex-direction: column;
        }

        /* Top Header */
        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 1rem 2rem;
            background: var(--panel-bg);
            backdrop-filter: blur(12px);
            border-bottom: 1px solid var(--panel-border);
            z-index: 10;
        }

        .logo-section {
            display: flex;
            align-items: center;
            gap: 0.75rem;
        }

        .logo-icon {
            width: 28px;
            height: 28px;
            background: linear-gradient(135deg, var(--primary), #a78bfa);
            border-radius: 8px;
            display: flex;
            align-items: center;
            justify-content: center;
            font-weight: 700;
            color: #fff;
            font-size: 0.9rem;
            box-shadow: 0 0 15px rgba(99, 102, 241, 0.4);
        }

        h1 {
            font-size: 1.25rem;
            font-weight: 600;
            letter-spacing: -0.025em;
        }

        .status-bar {
            display: flex;
            align-items: center;
            gap: 1.5rem;
            font-size: 0.875rem;
        }

        .stat-item {
            display: flex;
            align-items: center;
            gap: 0.5rem;
            color: var(--text-secondary);
        }

        .stat-val {
            font-weight: 600;
            color: var(--text-primary);
        }

        .status-dot {
            width: 8px;
            height: 8px;
            background-color: #10b981;
            border-radius: 50%;
            display: inline-block;
            box-shadow: 0 0 8px #10b981;
            animation: pulse 2s infinite;
        }

        @keyframes pulse {
            0% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.7); }
            70% { transform: scale(1); box-shadow: 0 0 0 6px rgba(16, 185, 129, 0); }
            100% { transform: scale(0.95); box-shadow: 0 0 0 0 rgba(16, 185, 129, 0); }
        }

        /* Main Workspace Container */
        .workspace {
            display: flex;
            flex: 1;
            overflow: hidden;
            position: relative;
        }

        /* Sidebar - Requests List */
        .sidebar {
            width: 380px;
            background: rgba(15, 22, 38, 0.4);
            border-right: 1px solid var(--panel-border);
            display: flex;
            flex-direction: column;
            overflow: hidden;
        }

        .search-container {
            padding: 1rem;
            border-bottom: 1px solid var(--panel-border);
        }

        .search-input {
            width: 100%;
            padding: 0.625rem 0.875rem;
            background: rgba(255, 255, 255, 0.05);
            border: 1px solid var(--panel-border);
            border-radius: 8px;
            color: var(--text-primary);
            font-family: inherit;
            font-size: 0.875rem;
            outline: none;
            transition: all 0.2s;
        }

        .search-input:focus {
            border-color: var(--primary);
            background: rgba(255, 255, 255, 0.08);
            box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.15);
        }

        .requests-list {
            flex: 1;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
        }

        .request-item {
            padding: 1rem;
            border-bottom: 1px solid rgba(255, 255, 255, 0.03);
            cursor: pointer;
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
            transition: all 0.15s ease;
            position: relative;
        }

        .request-item:hover {
            background: rgba(255, 255, 255, 0.02);
        }

        .request-item.active {
            background: rgba(99, 102, 241, 0.08);
            border-left: 3px solid var(--primary);
            padding-left: 13px; /* Offset the border width to keep spacing perfect */
        }

        .request-item-top {
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .method-path {
            display: flex;
            align-items: center;
            gap: 0.625rem;
            overflow: hidden;
            flex: 1;
        }

        .badge {
            font-size: 0.75rem;
            font-weight: 700;
            padding: 0.15rem 0.4rem;
            border-radius: 4px;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            display: inline-block;
            flex-shrink: 0;
        }

        .badge-GET { background: var(--badge-get-bg); color: var(--badge-get-text); }
        .badge-POST { background: var(--badge-post-bg); color: var(--badge-post-text); }
        .badge-PUT { background: var(--badge-put-bg); color: var(--badge-put-text); }
        .badge-DELETE { background: var(--badge-delete-bg); color: var(--badge-delete-text); }
        .badge-PATCH { background: var(--badge-put-bg); color: var(--badge-put-text); }
        .badge-other { background: var(--badge-other-bg); color: var(--badge-other-text); }

        .path {
            font-size: 0.875rem;
            font-weight: 500;
            color: var(--text-primary);
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .status-badge {
            font-size: 0.75rem;
            font-weight: 600;
            padding: 0.15rem 0.4rem;
            border-radius: 4px;
            border: 1px solid transparent;
        }

        .status-2xx { background: var(--status-2xx-bg); color: var(--status-2xx-text); border-color: var(--status-2xx-border); }
        .status-3xx { background: var(--status-3xx-bg); color: var(--status-3xx-text); border-color: var(--status-3xx-border); }
        .status-4xx { background: var(--status-4xx-bg); color: var(--status-4xx-text); border-color: var(--status-4xx-border); }
        .status-5xx { background: var(--status-5xx-bg); color: var(--status-5xx-text); border-color: var(--status-5xx-border); }
        .status-pending { background: var(--status-pending-bg); color: var(--status-pending-text); border-color: var(--status-pending-border); }

        .request-item-bottom {
            display: flex;
            justify-content: space-between;
            align-items: center;
            font-size: 0.75rem;
            color: var(--text-muted);
        }

        .latency {
            font-weight: 500;
            color: var(--text-secondary);
        }

        /* Detail Panel - Right side */
        .detail-panel {
            flex: 1;
            display: flex;
            flex-direction: column;
            background: var(--panel-bg);
            backdrop-filter: blur(12px);
            overflow: hidden;
        }

        .empty-state {
            flex: 1;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            color: var(--text-muted);
            gap: 1rem;
        }

        .empty-icon {
            width: 48px;
            height: 48px;
            opacity: 0.3;
            color: var(--text-secondary);
        }

        .detail-header {
            padding: 1.5rem 2rem;
            border-bottom: 1px solid var(--panel-border);
            display: flex;
            justify-content: space-between;
            align-items: flex-start;
        }

        .detail-title-section {
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
            flex: 1;
            overflow: hidden;
        }

        .detail-title-row {
            display: flex;
            align-items: center;
            gap: 0.75rem;
            overflow: hidden;
        }

        .detail-path {
            font-size: 1.25rem;
            font-weight: 600;
            color: var(--text-primary);
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        .detail-meta-row {
            display: flex;
            gap: 1.5rem;
            font-size: 0.8125rem;
            color: var(--text-secondary);
        }

        .tabs-header {
            display: flex;
            background: rgba(15, 22, 38, 0.4);
            padding: 0.25rem;
            border-radius: 8px;
            border: 1px solid var(--panel-border);
        }

        .tab-btn {
            background: transparent;
            border: none;
            color: var(--text-secondary);
            padding: 0.5rem 1rem;
            font-size: 0.875rem;
            font-weight: 500;
            border-radius: 6px;
            cursor: pointer;
            transition: all 0.2s;
            font-family: inherit;
        }

        .tab-btn.active {
            background: rgba(255, 255, 255, 0.08);
            color: var(--text-primary);
            box-shadow: 0 1px 3px rgba(0,0,0,0.2);
        }

        .detail-content {
            flex: 1;
            overflow-y: auto;
            padding: 2rem;
            display: flex;
            flex-direction: column;
            gap: 2rem;
        }

        .section-title {
            font-size: 0.875rem;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            color: var(--text-secondary);
            margin-bottom: 0.75rem;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .headers-grid {
            display: grid;
            grid-template-columns: 240px 1fr;
            border: 1px solid var(--panel-border);
            border-radius: 8px;
            overflow: hidden;
            background: rgba(15, 22, 38, 0.2);
        }

        .header-key {
            padding: 0.625rem 1rem;
            font-weight: 600;
            color: var(--text-secondary);
            border-bottom: 1px solid var(--panel-border);
            border-right: 1px solid var(--panel-border);
            font-size: 0.8125rem;
            overflow-wrap: break-word;
        }

        .header-val {
            padding: 0.625rem 1rem;
            color: var(--text-primary);
            border-bottom: 1px solid var(--panel-border);
            font-size: 0.8125rem;
            font-family: 'Fira Code', monospace;
            overflow-wrap: break-word;
        }

        .headers-grid div:last-child, 
        .headers-grid div:nth-last-child(2) {
            border-bottom: none;
        }

        .body-wrapper {
            position: relative;
            border: 1px solid var(--panel-border);
            border-radius: 8px;
            background: rgba(10, 15, 30, 0.6);
            overflow: hidden;
        }

        .body-pre {
            padding: 1.25rem;
            font-family: 'Fira Code', monospace;
            font-size: 0.8125rem;
            color: #34d399;
            overflow-x: auto;
            max-height: 400px;
            white-space: pre-wrap;
            word-break: break-all;
        }

        .copy-btn {
            position: absolute;
            top: 0.75rem;
            right: 0.75rem;
            background: rgba(255, 255, 255, 0.05);
            border: 1px solid var(--panel-border);
            color: var(--text-secondary);
            padding: 0.35rem 0.75rem;
            font-size: 0.75rem;
            border-radius: 6px;
            cursor: pointer;
            transition: all 0.2s;
            font-family: inherit;
        }

        .copy-btn:hover {
            background: rgba(255, 255, 255, 0.1);
            color: var(--text-primary);
            border-color: rgba(255, 255, 255, 0.2);
        }

        .empty-body {
            padding: 1.5rem;
            text-align: center;
            color: var(--text-muted);
            font-style: italic;
            font-size: 0.875rem;
        }

        /* Scrollbars */
        ::-webkit-scrollbar {
            width: 8px;
            height: 8px;
        }
        ::-webkit-scrollbar-track {
            background: transparent;
        }
        ::-webkit-scrollbar-thumb {
            background: rgba(255, 255, 255, 0.1);
            border-radius: 4px;
        }
        ::-webkit-scrollbar-thumb:hover {
            background: rgba(255, 255, 255, 0.2);
        }
    </style>
</head>
<body>
    <header>
        <div class="logo-section">
            <div class="logo-icon">T</div>
            <h1>ServTunnel Client Inspector</h1>
        </div>
        <div class="status-bar">
            <div class="stat-item">
                <span class="status-dot"></span>
                <span>Client Status:</span>
                <span class="stat-val" style="color: #10b981;">Connected</span>
            </div>
            <div class="stat-item">
                <span>Total Requests:</span>
                <span class="stat-val" id="total-count">0</span>
            </div>
        </div>
    </header>

    <div class="workspace">
        <!-- Requests Sidebar -->
        <div class="sidebar">
            <div class="search-container">
                <input type="text" id="search" class="search-input" placeholder="Filter requests by path..." autocomplete="off">
            </div>
            <div class="requests-list" id="requests-list">
                <!-- Requests injected here -->
            </div>
        </div>

        <!-- Detail Panel -->
        <div class="detail-panel" id="detail-panel">
            <div class="empty-state">
                <svg class="empty-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"></path>
                </svg>
                <p>Select a request from the sidebar to inspect its details</p>
            </div>
        </div>
    </div>

    <script>
        var requests = [];
        var selectedId = null;
        var activeTab = "request";

        var searchInput = document.getElementById("search");
        var listContainer = document.getElementById("requests-list");
        var detailPanel = document.getElementById("detail-panel");
        var totalCountVal = document.getElementById("total-count");

        async function fetchRequests() {
            try {
                var response = await fetch("/api/inspect");
                if (!response.ok) return;
                var data = await response.json();
                
                var newRequests = (data.entries || []).reverse();
                totalCountVal.textContent = data.total || newRequests.length;

                var listChanged = JSON.stringify(newRequests) !== JSON.stringify(requests);
                if (listChanged) {
                    requests = newRequests;
                    renderSidebar();
                    if (selectedId) {
                        var updatedSelected = requests.find(function(r) { return r.id === selectedId; });
                        if (updatedSelected) {
                            renderDetails(updatedSelected);
                        }
                    }
                }
            } catch (err) {
                console.error("Failed to fetch requests", err);
            }
        }

        function getStatusClass(status) {
            if (!status) return "status-pending";
            if (status >= 200 && status < 300) return "status-2xx";
            if (status >= 300 && status < 400) return "status-3xx";
            if (status >= 400 && status < 500) return "status-4xx";
            return "status-5xx";
        }

        function formatTime(timestampStr) {
            if (!timestampStr) return "";
            var d = new Date(timestampStr);
            return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
        }

        function decodeBody(b64Str) {
            if (!b64Str) return "";
            try {
                var binString = atob(b64Str);
                var bytes = Uint8Array.from(binString, function(m) { return m.codePointAt(0); });
                var decoded = new TextDecoder().decode(bytes);
                try {
                    var json = JSON.parse(decoded);
                    return JSON.stringify(json, null, 2);
                } catch (e) {
                    return decoded;
                }
            } catch (e) {
                return b64Str;
            }
        }

        function renderSidebar() {
            var query = searchInput.value.toLowerCase();
            var filtered = requests.filter(function(r) { return r.path.toLowerCase().indexOf(query) !== -1; });

            listContainer.innerHTML = "";
            if (filtered.length === 0) {
                listContainer.innerHTML = "<div style=\"padding:2rem; text-align:center; color:var(--text-muted); font-size:0.875rem;\">No requests captured</div>";
                return;
            }

            filtered.forEach(function(req) {
                var item = document.createElement("div");
                item.className = "request-item " + (req.id === selectedId ? "active" : "");
                item.onclick = function() { selectRequest(req.id); };

                var badgeType = ["GET", "POST", "PUT", "DELETE", "PATCH"].indexOf(req.method) !== -1 ? req.method : "other";
                
                item.innerHTML = 
                    "<div class=\"request-item-top\">" +
                        "<div class=\"method-path\">" +
                            "<span class=\"badge badge-" + badgeType + "\">" + req.method + "</span>" +
                            "<span class=\"path\" title=\"" + req.path + "\">" + req.path + "</span>" +
                        "</div>" +
                        "<span class=\"status-badge " + getStatusClass(req.status_code) + "\">" + (req.status_code || "...") + "</span>" +
                    "</div>" +
                    "<div class=\"request-item-bottom\">" +
                        "<span>" + formatTime(req.timestamp) + "</span>" +
                        "<span class=\"latency\">" + (req.latency_ms ? req.latency_ms + "ms" : "") + "</span>" +
                    "</div>";
                listContainer.appendChild(item);
            });
        }

        function selectRequest(id) {
            selectedId = id;
            renderSidebar();
            var req = requests.find(function(r) { return r.id === id; });
            if (req) {
                renderDetails(req);
            }
        }

        function copyToClipboard(textId) {
            var text = document.getElementById(textId).innerText;
            navigator.clipboard.writeText(text).then(function() {
                var btn = document.querySelector("[onclick=\"copyToClipboard('" + textId + "')\"]");
                var oldText = btn.textContent;
                btn.textContent = "Copied!";
                setTimeout(function() { btn.textContent = oldText; }, 1500);
            });
        }

        function renderDetails(req) {
            var badgeType = ["GET", "POST", "PUT", "DELETE", "PATCH"].indexOf(req.method) !== -1 ? req.method : "other";
            var statusText = req.status_code ? "Status " + req.status_code : "Pending";

            var tabContentHTML = "";
            if (activeTab === "request") {
                var headersHTML = "";
                if (req.request_headers && Object.keys(req.request_headers).length > 0) {
                    headersHTML = 
                        "<div>" +
                            "<div class=\"section-title\">Request Headers</div>" +
                            "<div class=\"headers-grid\">" +
                                Object.entries(req.request_headers).map(function(pair) {
                                    return "<div class=\"header-key\">" + pair[0] + "</div>" +
                                           "<div class=\"header-val\">" + pair[1] + "</div>";
                                }).join("") +
                            "</div>" +
                        "</div>";
                }

                var bodyHTML = "";
                if (req.request_body) {
                    var decoded = decodeBody(req.request_body);
                    bodyHTML = 
                        "<div>" +
                            "<div class=\"section-title\">" +
                                "<span>Request Body</span>" +
                                "<button class=\"copy-btn\" onclick=\"copyToClipboard('req-body-text')\">Copy</button>" +
                            "</div>" +
                            "<div class=\"body-wrapper\">" +
                                "<pre class=\"body-pre\" id=\"req-body-text\">" + escapeHtml(decoded) + "</pre>" +
                            "</div>" +
                        "</div>";
                } else {
                    bodyHTML = 
                        "<div>" +
                            "<div class=\"section-title\">Request Body</div>" +
                            "<div class=\"body-wrapper\">" +
                                "<div class=\"empty-body\">No request body</div>" +
                            "</div>" +
                        "</div>";
                }

                tabContentHTML = headersHTML + bodyHTML;
            } else {
                var headersHTML = "";
                if (req.response_headers && Object.keys(req.response_headers).length > 0) {
                    headersHTML = 
                        "<div>" +
                            "<div class=\"section-title\">Response Headers</div>" +
                            "<div class=\"headers-grid\">" +
                                Object.entries(req.response_headers).map(function(pair) {
                                    return "<div class=\"header-key\">" + pair[0] + "</div>" +
                                           "<div class=\"header-val\">" + pair[1] + "</div>";
                                }).join("") +
                            "</div>" +
                        "</div>";
                }

                var bodyHTML = "";
                if (req.response_body) {
                    var decoded = decodeBody(req.response_body);
                    bodyHTML = 
                        "<div>" +
                            "<div class=\"section-title\">" +
                                "<span>Response Body</span>" +
                                "<button class=\"copy-btn\" onclick=\"copyToClipboard('resp-body-text')">Copy</button>" +
                            "</div>" +
                            "<div class=\"body-wrapper\">" +
                                "<pre class=\"body-pre\" id=\"resp-body-text\">" + escapeHtml(decoded) + "</pre>" +
                            "</div>" +
                        "</div>";
                } else {
                    bodyHTML = 
                        "<div>" +
                            "<div class=\"section-title\">Response Body</div>" +
                            "<div class=\"body-wrapper\">" +
                                "<div class=\"empty-body\">" + (req.status_code ? "No response body" : "Waiting for response...") + "</div>" +
                            "</div>" +
                        "</div>";
                }

                tabContentHTML = headersHTML + bodyHTML;
            }

            detailPanel.innerHTML = 
                "<div class=\"detail-header\">" +
                    "<div class=\"detail-title-section\">" +
                        "<div class=\"detail-title-row\">" +
                            "<span class=\"badge badge-" + badgeType + "\">" + req.method + "</span>" +
                            "<span class=\"detail-path\" title=\"" + req.path + "\">" + req.path + "</span>" +
                        "</div>" +
                        "<div class=\"detail-meta-row\">" +
                            "<span>ID: <strong>" + req.id + "</strong></span>" +
                            "<span>Time: <strong>" + new Date(req.timestamp).toLocaleString() + "</strong></span>" +
                            (req.latency_ms ? "<span>Latency: <strong>" + req.latency_ms + "ms</strong></span>" : "") +
                            "<span>Status: <strong class=\"" + (req.status_code >= 400 ? "status-5xx-text" : "status-2xx-text") + "\">" + (req.status_code || "Pending") + "</strong></span>" +
                        "</div>" +
                    "</div>" +
                    "<div class=\"tabs-header\">" +
                        "<button class=\"tab-btn " + (activeTab === "request" ? "active" : "") + "\" onclick=\"switchTab('request')\">Request</button>" +
                        "<button class=\"tab-btn " + (activeTab === "response" ? "active" : "") + "\" onclick=\"switchTab('response')\">Response</button>" +
                    "</div>" +
                "</div>" +
                "<div class=\"detail-content\">" +
                    tabContentHTML +
                "</div>";
        }

        function switchTab(tab) {
            activeTab = tab;
            if (selectedId) {
                var req = requests.find(function(r) { return r.id === selectedId; });
                if (req) renderDetails(req);
            }
        }

        function escapeHtml(str) {
            return str
                .replace(/&/g, "&amp;")
                .replace(/</g, "&lt;")
                .replace(/>/g, "&gt;")
                .replace(/"/g, "&quot;")
                .replace(/'/g, "&#039;");
        }

        searchInput.addEventListener("input", renderSidebar);

        fetchRequests();
        setInterval(fetchRequests, 1500);
    </script>
</body>
</html>`
