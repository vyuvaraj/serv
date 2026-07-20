// ServStore Console Application Logic
// Wrap global fetch to automatically inject Bearer tokens and handle 401/403 redirects
const originalFetch = window.fetch;
window.fetch = async function (resource, options = {}) {
    options.headers = options.headers || {};
    const token = localStorage.getItem('token');
    if (token) {
        // Only add authorization header for local requests, not external URLs
        if (typeof resource === 'string' && (!resource.startsWith('http') || resource.startsWith(window.location.origin))) {
            options.headers['Authorization'] = `Bearer ${token}`;
        }
    }
    
    const response = await originalFetch(resource, options);
    if ((response.status === 401 || response.status === 403) && !resource.includes('/console/login')) {
        localStorage.removeItem('token');
        window.location.href = '/login.html';
    }
    return response;
};

document.addEventListener('DOMContentLoaded', () => {
    let currentBucket = '';
    let selectedObjectForVersions = '';
    let activeTab = 'dashboard';

    // Elements
    const bucketListEl = document.getElementById('bucket-list');
    const noBucketEl = document.getElementById('no-bucket-selected');
    const bucketViewEl = document.getElementById('bucket-view');
    const currentBucketNameEl = document.getElementById('current-bucket-name');
    const bucketCreatedTimeEl = document.getElementById('bucket-created-time');
    const versioningToggle = document.getElementById('toggle-versioning');
    const versioningStatusEl = document.getElementById('versioning-status');
    const objectListBody = document.getElementById('object-list-body');
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const uploadProgressContainer = document.getElementById('upload-progress-container');
    const uploadFilenameEl = document.getElementById('upload-filename');
    const uploadPercentageEl = document.getElementById('upload-percentage');
    const uploadProgressBar = document.getElementById('upload-progress-bar');
    const searchInput = document.getElementById('object-search');

    // Modals
    const modalCreateBucket = document.getElementById('modal-create-bucket');
    const modalVersions = document.getElementById('modal-versions');
    const versionListBody = document.getElementById('version-list-body');
    const versionModalFilename = document.getElementById('version-modal-filename');

    // Tab panes and nav items
    const navItems = document.querySelectorAll('.nav-item');
    const tabPanes = document.querySelectorAll('.tab-pane');
    const tabTitleEl = document.getElementById('tab-title');
    const breadcrumbSeparator = document.getElementById('breadcrumb-separator');
    const breadcrumbBucket = document.getElementById('breadcrumb-bucket');

    // Toast Notification helper
    function showToast(message, type = 'success') {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        toast.className = `toast toast-${type}`;
        
        const icon = type === 'success' ? 'fa-circle-check' : 'fa-circle-exclamation';
        toast.innerHTML = `
            <i class="fa-solid ${icon}"></i>
            <span>${message}</span>
        `;
        
        container.appendChild(toast);
        setTimeout(() => {
            toast.style.opacity = '0';
            toast.style.transform = 'translateY(20px)';
            setTimeout(() => toast.remove(), 300);
        }, 3000);
    }

    // Helper to format bytes
    function formatBytes(bytes, decimals = 2) {
        if (!bytes || bytes === 0) return '0 Bytes';
        const k = 1024;
        const dm = decimals < 0 ? 0 : decimals;
        const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
    }

    // XML helper to get node value safely
    function getXmlValue(parent, tagName, defaultValue = '') {
        const els = parent.getElementsByTagName(tagName);
        if (els && els.length > 0) {
            return els[0].textContent;
        }
        const allNodes = parent.querySelectorAll('*');
        for (let node of allNodes) {
            if (node.localName === tagName) {
                return node.textContent;
            }
        }
        return defaultValue;
    }

    // --- Tab Switching Logic ---
    navItems.forEach(item => {
        item.addEventListener('click', (e) => {
            e.preventDefault();
            const tab = item.getAttribute('data-tab');
            switchTab(tab);
        });
    });

    function switchTab(tab) {
        activeTab = tab;
        navItems.forEach(item => {
            if (item.getAttribute('data-tab') === tab) {
                item.classList.add('active');
            } else {
                item.classList.remove('active');
            }
        });

        tabPanes.forEach(pane => {
            if (pane.id === `tab-pane-${tab}`) {
                pane.classList.add('active');
            } else {
                pane.classList.remove('active');
            }
        });

        // Update breadcrumb
        const displayNames = {
            dashboard: 'Dashboard',
            browser: 'Object Browser',
            ring: 'Consistent Ring',
            traces: 'OTEL Traces',
            policies: 'Access Control'
        };
        tabTitleEl.textContent = displayNames[tab] || tab;

        if (tab === 'browser' && currentBucket) {
            breadcrumbSeparator.style.display = 'inline';
            breadcrumbBucket.style.display = 'inline';
            breadcrumbBucket.textContent = currentBucket;
        } else {
            breadcrumbSeparator.style.display = 'none';
            breadcrumbBucket.style.display = 'none';
        }

        // Trigger tab-specific loads
        if (tab === 'traces') {
            loadTraces();
        } else if (tab === 'ring') {
            loadRing();
        } else if (tab === 'policies') {
            loadPoliciesView();
        }
    }

    // --- S3 Bucket & Object Browser Logic ---
    async function loadBuckets() {
        bucketListEl.innerHTML = '<div class="loading-spinner"><i class="fa-solid fa-circle-notch fa-spin"></i></div>';
        try {
            const res = await fetch('/');
            if (!res.ok) throw new Error('Failed to load buckets');
            const text = await res.text();
            
            const parser = new DOMParser();
            const xmlDoc = parser.parseFromString(text, 'text/xml');
            
            const buckets = [];
            const bucketNodes = xmlDoc.querySelectorAll('Bucket');
            bucketNodes.forEach(node => {
                buckets.push({
                    name: getXmlValue(node, 'Name'),
                    created: getXmlValue(node, 'CreationDate')
                });
            });

            if (buckets.length === 0) {
                bucketListEl.innerHTML = '<div class="empty-state-small" style="padding: 16px; text-align: center; color: var(--text-muted);">No buckets found.</div>';
                return;
            }

            bucketListEl.innerHTML = '';
            buckets.forEach(b => {
                const item = document.createElement('div');
                item.className = `bucket-item ${currentBucket === b.name ? 'active' : ''}`;
                item.innerHTML = `
                    <i class="fa-solid fa-bucket"></i>
                    <span>${b.name}</span>
                `;
                item.addEventListener('click', () => {
                    selectBucket(b.name, b.created);
                    switchTab('browser');
                });
                bucketListEl.appendChild(item);
            });
        } catch (err) {
            bucketListEl.innerHTML = `<div class="error-text" style="color: var(--danger-color); padding: 16px;">Error: ${err.message}</div>`;
        }
    }

    async function selectBucket(name, createdTime) {
        currentBucket = name;
        
        breadcrumbBucket.textContent = name;
        if (activeTab === 'browser') {
            breadcrumbSeparator.style.display = 'inline';
            breadcrumbBucket.style.display = 'inline';
        }

        currentBucketNameEl.textContent = name;
        if (createdTime) {
            const date = new Date(createdTime);
            bucketCreatedTimeEl.textContent = `Created: ${date.toLocaleString()}`;
        } else {
            bucketCreatedTimeEl.textContent = '';
        }

        const items = bucketListEl.querySelectorAll('.bucket-item');
        items.forEach(item => {
            if (item.querySelector('span').textContent === name) {
                item.classList.add('active');
            } else {
                item.classList.remove('active');
            }
        });

        noBucketEl.style.display = 'none';
        bucketViewEl.style.display = 'flex';

        await loadVersioningStatus();
        await loadObjects();
    }

    async function loadVersioningStatus() {
        try {
            const res = await fetch(`/${currentBucket}?versioning`);
            if (!res.ok) return;
            const text = await res.text();
            
            const parser = new DOMParser();
            const xmlDoc = parser.parseFromString(text, 'text/xml');
            const status = getXmlValue(xmlDoc, 'Status', 'Disabled');
            
            versioningToggle.checked = (status === 'Enabled');
            updateVersioningBadge(status);
        } catch (err) {
            console.error('Error fetching versioning status:', err);
        }
    }

    function updateVersioningBadge(status) {
        versioningStatusEl.textContent = status;
        versioningStatusEl.className = 'status-badge';
        if (status === 'Enabled') {
            versioningStatusEl.classList.add('enabled');
        }
    }

    async function setVersioning(enabled) {
        const status = enabled ? 'Enabled' : 'Suspended';
        const xmlBody = `<?xml version="1.0" encoding="UTF-8"?>
<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Status>${status}</Status>
</VersioningConfiguration>`;

        try {
            const res = await fetch(`/${currentBucket}?versioning`, {
                method: 'PUT',
                body: xmlBody,
                headers: {
                    'Content-Type': 'application/xml'
                }
            });
            if (!res.ok) throw new Error('Failed to set versioning status');
            updateVersioningBadge(status);
            showToast(`Bucket versioning set to ${status}`, 'success');
        } catch (err) {
            showToast(err.message, 'error');
            versioningToggle.checked = !enabled;
        }
    }

    async function loadObjects() {
        objectListBody.innerHTML = '<tr><td colspan="5"><div class="loading-spinner"><i class="fa-solid fa-circle-notch fa-spin"></i></div></td></tr>';
        try {
            const res = await fetch(`/${currentBucket}`);
            if (!res.ok) throw new Error('Failed to fetch bucket contents');
            const text = await res.text();
            
            const parser = new DOMParser();
            const xmlDoc = parser.parseFromString(text, 'text/xml');
            
            const contents = xmlDoc.querySelectorAll('Contents');
            const objects = [];
            contents.forEach(node => {
                objects.push({
                    key: getXmlValue(node, 'Key'),
                    size: parseInt(getXmlValue(node, 'Size'), 10),
                    lastModified: getXmlValue(node, 'LastModified'),
                    etag: getXmlValue(node, 'ETag').replace(/"/g, '')
                });
            });

            renderObjects(objects);
        } catch (err) {
            objectListBody.innerHTML = `<tr><td colspan="5" style="color: var(--danger-color); text-align: center; padding: 20px;">Error loading objects: ${err.message}</td></tr>`;
        }
    }

    function renderObjects(objects) {
        if (objects.length === 0) {
            objectListBody.innerHTML = `<tr><td colspan="5" style="text-align: center; padding: 40px; color: var(--text-muted);">
                <i class="fa-regular fa-folder-open" style="font-size: 24px; margin-bottom: 8px; display: block;"></i>
                This bucket is empty. Drag & drop files above to upload.
            </td></tr>`;
            return;
        }

        objectListBody.innerHTML = '';
        objects.forEach(obj => {
            const date = new Date(obj.lastModified);
            const tr = document.createElement('tr');
            
            tr.innerHTML = `
                <td>
                    <div class="file-name-cell">
                        <i class="fa-regular fa-file file-icon"></i>
                        <span>${obj.key}</span>
                    </div>
                </td>
                <td>${formatBytes(obj.size)}</td>
                <td>${date.toLocaleString()}</td>
                <td style="font-family: monospace; font-size: 12px; color: var(--text-muted);">${obj.etag}</td>
                <td>
                    <div class="action-buttons">
                        <a href="/${currentBucket}/${obj.key}" download="${obj.key}" class="btn-table-action" title="Download">
                            <i class="fa-solid fa-download"></i>
                        </a>
                        <button class="btn-table-action btn-versions" title="Version History">
                            <i class="fa-solid fa-clock-rotate-left"></i>
                        </button>
                        <button class="btn-table-action delete btn-delete-obj" title="Delete">
                            <i class="fa-solid fa-trash"></i>
                        </button>
                    </div>
                </td>
            `;

            tr.querySelector('.btn-versions').addEventListener('click', () => openVersionsModal(obj.key));
            tr.querySelector('.btn-delete-obj').addEventListener('click', () => deleteObject(obj.key));

            objectListBody.appendChild(tr);
        });
    }

    async function deleteObject(key) {
        if (!confirm(`Are you sure you want to delete "${key}"?`)) return;
        try {
            const res = await fetch(`/${currentBucket}/${key}`, {
                method: 'DELETE'
            });
            if (!res.ok) throw new Error('Failed to delete object');
            showToast(`Object "${key}" deleted`, 'success');
            loadObjects();
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    async function createBucket(name) {
        const regex = /^[a-z0-9.-]{3,63}$/;
        if (!regex.test(name)) {
            showToast('Invalid bucket name. Use 3-63 lowercase alphanumeric characters or hyphens.', 'error');
            return;
        }

        try {
            const res = await fetch(`/${name}`, {
                method: 'PUT'
            });
            if (!res.ok) {
                const text = await res.text();
                const parser = new DOMParser();
                const xmlDoc = parser.parseFromString(text, 'text/xml');
                const errMsg = getXmlValue(xmlDoc, 'Message', 'Failed to create bucket');
                throw new Error(errMsg);
            }
            showToast(`Bucket "${name}" created successfully!`, 'success');
            modalCreateBucket.classList.remove('show');
            document.getElementById('new-bucket-name').value = '';
            await loadBuckets();
            selectBucket(name);
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    async function deleteCurrentBucket() {
        if (!confirm(`Are you sure you want to delete the bucket "${currentBucket}"? This action cannot be undone.`)) return;
        try {
            const res = await fetch(`/${currentBucket}`, {
                method: 'DELETE'
            });
            if (!res.ok) {
                const text = await res.text();
                const parser = new DOMParser();
                const xmlDoc = parser.parseFromString(text, 'text/xml');
                const errMsg = getXmlValue(xmlDoc, 'Message', 'Failed to delete bucket. Make sure it is empty.');
                throw new Error(errMsg);
            }
            showToast(`Bucket "${currentBucket}" deleted`, 'success');
            currentBucket = '';
            noBucketEl.style.display = 'flex';
            bucketViewEl.style.display = 'none';
            breadcrumbBucket.textContent = 'Select a bucket';
            breadcrumbSeparator.style.display = 'none';
            breadcrumbBucket.style.display = 'none';
            await loadBuckets();
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    function performUpload(file) {
        uploadProgressContainer.style.display = 'block';
        uploadFilenameEl.textContent = file.name;
        uploadPercentageEl.textContent = '0%';
        uploadProgressBar.style.width = '0%';

        const xhr = new XMLHttpRequest();
        xhr.open('PUT', `/${currentBucket}/${file.name}`, true);

        const token = localStorage.getItem('token');
        if (token) {
            xhr.setRequestHeader('Authorization', `Bearer ${token}`);
        }

        xhr.upload.onprogress = (e) => {
            if (e.lengthComputable) {
                const percent = Math.round((e.loaded / e.total) * 100);
                uploadPercentageEl.textContent = `${percent}%`;
                uploadProgressBar.style.width = `${percent}%`;
            }
        };

        xhr.onload = () => {
            if (xhr.status >= 200 && xhr.status < 300) {
                showToast(`Uploaded "${file.name}" successfully`, 'success');
                loadObjects();
            } else {
                showToast(`Upload failed for "${file.name}"`, 'error');
            }
            setTimeout(() => {
                uploadProgressContainer.style.display = 'none';
            }, 1000);
        };

        xhr.onerror = () => {
            showToast(`Upload failed for "${file.name}"`, 'error');
            uploadProgressContainer.style.display = 'none';
        };

        xhr.send(file);
    }

    dropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropZone.classList.add('dragover');
    });

    dropZone.addEventListener('dragleave', () => {
        dropZone.classList.remove('dragover');
    });

    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropZone.classList.remove('dragover');
        if (e.dataTransfer.files.length > 0) {
            performUpload(e.dataTransfer.files[0]);
        }
    });

    dropZone.addEventListener('click', () => {
        fileInput.click();
    });

    fileInput.addEventListener('change', () => {
        if (fileInput.files.length > 0) {
            performUpload(fileInput.files[0]);
        }
    });

    async function openVersionsModal(key) {
        selectedObjectForVersions = key;
        versionModalFilename.textContent = key;
        versionListBody.innerHTML = '<tr><td colspan="6"><div class="loading-spinner"><i class="fa-solid fa-circle-notch fa-spin"></i></div></td></tr>';
        modalVersions.classList.add('show');
        await loadVersions();
    }

    async function loadVersions() {
        try {
            const res = await fetch(`/${currentBucket}?versions&prefix=${selectedObjectForVersions}`);
            if (!res.ok) throw new Error('Failed to load version history');
            const text = await res.text();

            const parser = new DOMParser();
            const xmlDoc = parser.parseFromString(text, 'text/xml');
            
            const versions = [];
            
            const versionNodes = xmlDoc.querySelectorAll('Version');
            versionNodes.forEach(node => {
                versions.push({
                    versionId: getXmlValue(node, 'VersionId'),
                    isLatest: getXmlValue(node, 'IsLatest') === 'true',
                    size: parseInt(getXmlValue(node, 'Size'), 10),
                    lastModified: getXmlValue(node, 'LastModified'),
                    etag: getXmlValue(node, 'ETag').replace(/"/g, ''),
                    isDeleteMarker: false
                });
            });

            const deleteMarkerNodes = xmlDoc.querySelectorAll('DeleteMarker');
            deleteMarkerNodes.forEach(node => {
                versions.push({
                    versionId: getXmlValue(node, 'VersionId'),
                    isLatest: getXmlValue(node, 'IsLatest') === 'true',
                    size: 0,
                    lastModified: getXmlValue(node, 'LastModified'),
                    etag: '',
                    isDeleteMarker: true
                });
            });

            versions.sort((a, b) => new Date(b.lastModified) - new Date(a.lastModified));
            renderVersions(versions);
        } catch (err) {
            versionListBody.innerHTML = `<tr><td colspan="6" style="color: var(--danger-color); text-align: center; padding: 20px;">Error: ${err.message}</td></tr>`;
        }
    }

    function renderVersions(versions) {
        if (versions.length === 0) {
            versionListBody.innerHTML = '<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--text-muted);">No versions found.</td></tr>';
            return;
        }

        versionListBody.innerHTML = '';
        versions.forEach(v => {
            const date = new Date(v.lastModified);
            const tr = document.createElement('tr');
            if (v.isDeleteMarker) {
                tr.className = 'delete-marker-row';
            }

            let statusBadge = '';
            if (v.isLatest) statusBadge += '<span class="badge-latest">Latest</span> ';
            if (v.isDeleteMarker) statusBadge += '<span class="badge-marker">Delete Marker</span>';

            const sizeDisplay = v.isDeleteMarker ? '-' : formatBytes(v.size);
            const etagDisplay = v.isDeleteMarker ? '-' : v.etag;

            tr.innerHTML = `
                <td><span class="badge-version">${v.versionId || 'null'}</span></td>
                <td>${statusBadge}</td>
                <td>${sizeDisplay}</td>
                <td>${date.toLocaleString()}</td>
                <td style="font-family: monospace; font-size: 11px; color: var(--text-muted);">${etagDisplay}</td>
                <td>
                    <div class="action-buttons">
                        ${!v.isDeleteMarker ? `
                            <a href="/${currentBucket}/${selectedObjectForVersions}?versionId=${v.versionId}" download="${selectedObjectForVersions}" class="btn-table-action" title="Download this version">
                                <i class="fa-solid fa-download"></i>
                            </a>
                        ` : ''}
                        <button class="btn-table-action delete btn-delete-ver" title="Permanently Delete version">
                            <i class="fa-solid fa-trash-can"></i>
                        </button>
                    </div>
                </td>
            `;

            tr.querySelector('.btn-delete-ver').addEventListener('click', () => deleteVersion(selectedObjectForVersions, v.versionId));
            versionListBody.appendChild(tr);
        });
    }

    async function deleteVersion(key, versionId) {
        if (!confirm(`Are you sure you want to permanently delete the version "${versionId || 'null'}" of "${key}"? This cannot be undone.`)) return;
        try {
            const res = await fetch(`/${currentBucket}/${key}?versionId=${versionId || 'null'}`, {
                method: 'DELETE'
            });
            if (!res.ok) throw new Error('Failed to delete version');
            showToast('Version permanently deleted', 'success');
            await loadVersions();
            await loadObjects();
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    // --- Real-time Metrics & Dashboard Polling ---
    let throughputChartObj = null;
    let latencyChartObj = null;
    
    let lastTotalRequests = null;
    let lastTotalDuration = 0;
    let lastTotalCount = 0;

    const maxHistoryPoints = 12;
    const throughputHistory = Array(maxHistoryPoints).fill(0);
    const latencyHistory = Array(maxHistoryPoints).fill(0);
    const labelsHistory = Array(maxHistoryPoints).fill('');

    function initCharts() {
        const commonOptions = {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                x: {
                    grid: { color: 'rgba(255, 255, 255, 0.05)' },
                    ticks: { color: '#94a3b8', font: { family: 'Plus Jakarta Sans', size: 10 } }
                },
                y: {
                    grid: { color: 'rgba(255, 255, 255, 0.05)' },
                    ticks: { color: '#94a3b8', font: { family: 'Plus Jakarta Sans', size: 10 } }
                }
            },
            plugins: {
                legend: { display: false }
            },
            elements: {
                line: { tension: 0.4 },
                point: { radius: 2, hoverRadius: 5 }
            }
        };

        const ctxThroughput = document.getElementById('throughput-chart').getContext('2d');
        const gradientT = ctxThroughput.createLinearGradient(0, 0, 0, 200);
        gradientT.addColorStop(0, 'rgba(99, 102, 241, 0.3)');
        gradientT.addColorStop(1, 'rgba(99, 102, 241, 0.0)');

        throughputChartObj = new Chart(ctxThroughput, {
            type: 'line',
            data: {
                labels: labelsHistory,
                datasets: [{
                    borderColor: '#6366f1',
                    borderWidth: 2,
                    backgroundColor: gradientT,
                    fill: true,
                    data: throughputHistory
                }]
            },
            options: commonOptions
        });

        const ctxLatency = document.getElementById('latency-chart').getContext('2d');
        const gradientL = ctxLatency.createLinearGradient(0, 0, 0, 200);
        gradientL.addColorStop(0, 'rgba(6, 182, 212, 0.3)');
        gradientL.addColorStop(1, 'rgba(6, 182, 212, 0.0)');

        latencyChartObj = new Chart(ctxLatency, {
            type: 'line',
            data: {
                labels: labelsHistory,
                datasets: [{
                    borderColor: '#06b6d4',
                    borderWidth: 2,
                    backgroundColor: gradientL,
                    fill: true,
                    data: latencyHistory
                }]
            },
            options: commonOptions
        });
    }

    async function pollMetrics() {
        try {
            const res = await fetch('/console/metrics');
            if (!res.ok) return;
            const data = await res.json();

            // In-flight requests
            const inFlight = data.http_in_flight_requests || 0;
            document.getElementById('metric-inflight-requests').textContent = inFlight;

            // Compute total HTTP requests
            let totalReqs = 0;
            if (data.http_requests_total) {
                Object.values(data.http_requests_total).forEach(val => {
                    totalReqs += parseInt(val, 10);
                });
            }
            document.getElementById('metric-total-requests').textContent = totalReqs;

            // Compute durations and counts for Latency
            let totalDur = 0;
            let totalCnt = 0;
            if (data.http_request_duration_seconds) {
                Object.values(data.http_request_duration_seconds).forEach(val => {
                    totalDur += parseFloat(val);
                });
            }
            if (data.http_request_duration_counts) {
                Object.values(data.http_request_duration_counts).forEach(val => {
                    totalCnt += parseInt(val, 10);
                });
            }

            // Calculate Throughput and Latency relative to last poll
            const now = new Date();
            const timeStr = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });

            let currentThroughput = 0;
            let currentLatency = 0;

            if (lastTotalRequests !== null) {
                // Polling runs every 2s
                const diffReq = totalReqs - lastTotalRequests;
                currentThroughput = parseFloat((diffReq / 2.0).toFixed(2));

                const diffCnt = totalCnt - lastTotalCount;
                if (diffCnt > 0) {
                    const diffDur = totalDur - lastTotalDuration;
                    currentLatency = parseFloat(((diffDur / diffCnt) * 1000).toFixed(1)); // to ms
                }
            }

            lastTotalRequests = totalReqs;
            lastTotalDuration = totalDur;
            lastTotalCount = totalCnt;

            // Push to rolling history arrays
            throughputHistory.shift();
            throughputHistory.push(currentThroughput);

            latencyHistory.shift();
            latencyHistory.push(currentLatency);

            labelsHistory.shift();
            labelsHistory.push(timeStr);

            // Update charts
            if (throughputChartObj && latencyChartObj) {
                throughputChartObj.update('none');
                latencyChartObj.update('none');
            }
        } catch (err) {
            console.error('Metrics poll error:', err);
        }
    }

    async function pollClusterStatus() {
        try {
            const res = await fetch('/console/cluster/status');
            if (!res.ok) return;
            const nodes = await res.json();

            // Total Node Count
            const nodeCount = nodes.length || 1;
            document.getElementById('metric-cluster-nodes').textContent = nodeCount;
            document.getElementById('metric-cluster-subtext').textContent = nodeCount > 1 
                ? `${nodeCount} active gossip nodes` 
                : 'Single node standalone mode';
            
            document.getElementById('node-status-text').textContent = nodeCount > 1
                ? `Cluster Active (${nodeCount} Nodes)`
                : 'Single-Node Active';

            const tbody = document.getElementById('gossip-node-list');
            if (!tbody) return;
            tbody.innerHTML = '';

            if (nodes.length === 0) {
                tbody.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-muted);">No cluster members active. Standalone node.</td></tr>`;
                return;
            }

            nodes.forEach(node => {
                const date = new Date(node.LastSeen || Date.now());
                const statusClass = node.Status === 'online' ? 'status-badge enabled' : 'status-badge';
                const statusDot = node.Status === 'online' ? '<span class="pulse-dot"></span>' : '';
                tbody.innerHTML += `
                    <tr>
                        <td style="font-weight: 600;">${node.NodeID}</td>
                        <td>${node.Address}</td>
                        <td><span class="badge-version" style="color: var(--text-primary);">${node.Region || 'default'}</span></td>
                        <td>
                            <div style="display: inline-flex; align-items: center; gap: 6px;">
                                ${statusDot}
                                <span class="${statusClass}">${node.Status}</span>
                            </div>
                        </td>
                        <td>${date.toLocaleTimeString()}</td>
                    </tr>
                `;
            });
        } catch (err) {
            console.error('Cluster status poll error:', err);
        }
    }

    // --- Consistent Hash Ring Rendering ---
    let ringNodes = [];
    let ringPartitions = {};

    function FNV1a64(str) {
        let hash = 14695981039346656037n;
        const prime = 1099511628211n;
        for (let i = 0; i < str.length; i++) {
            hash ^= BigInt(str.charCodeAt(i));
            hash = (hash * prime) & 0xffffffffffffffffn;
        }
        return hash;
    }

    async function loadRing() {
        const svg = document.getElementById('ring-svg');
        svg.innerHTML = '<circle cx="200" cy="200" r="30" stroke="var(--primary-color)" stroke-width="3" fill="none" class="fa-spin" />';
        
        try {
            const res = await fetch('/console/cluster/ring');
            if (!res.ok) throw new Error('Failed to fetch consistent ring status');
            const data = await res.json();

            ringNodes = data.nodes || [];
            ringPartitions = data.ring || {};

            renderRing(svg);
        } catch (err) {
            svg.innerHTML = `<text x="200" y="200" text-anchor="middle" fill="var(--danger-color)">Error: ${err.message}</text>`;
        }
    }

    const nodeColors = ['#6366f1', '#06b6d4', '#10b981', '#f59e0b', '#ef4444', '#ec4899', '#8b5cf6'];
    function getNodeColor(nodeId) {
        const index = ringNodes.indexOf(nodeId);
        if (index === -1) return '#64748b';
        return nodeColors[index % nodeColors.length];
    }

    function renderRing(svg, highlightedAngle = null, highlightedLabel = '') {
        svg.innerHTML = '';

        // Draw background main circle
        const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
        circle.setAttribute('cx', '200');
        circle.setAttribute('cy', '200');
        circle.setAttribute('r', '120');
        circle.setAttribute('stroke', 'rgba(255, 255, 255, 0.08)');
        circle.setAttribute('stroke-width', '8');
        circle.setAttribute('fill', 'none');
        svg.appendChild(circle);

        // Sort the ring partitions numerically
        const sortedPartitions = [];
        Object.entries(ringPartitions).forEach(([hashStr, nodeId]) => {
            sortedPartitions.push({
                hash: BigInt(hashStr),
                nodeId: nodeId
            });
        });
        sortedPartitions.sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0));

        const ringLimit = 2n ** 64n;

        // Draw virtual nodes
        sortedPartitions.forEach(p => {
            // angle from 0 to 2*PI, offset by -PI/2 to start at top
            const angle = Number(p.hash * 1000000n / ringLimit) / 1000000.0 * 2 * Math.PI - Math.PI / 2;
            const x = 200 + 120 * Math.cos(angle);
            const y = 200 + 120 * Math.sin(angle);

            const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
            dot.setAttribute('cx', x.toString());
            dot.setAttribute('cy', y.toString());
            dot.setAttribute('r', '4');
            dot.setAttribute('fill', getNodeColor(p.nodeId));
            dot.setAttribute('cursor', 'pointer');
            
            // Add SVG tooltip
            const title = document.createElementNS('http://www.w3.org/2000/svg', 'title');
            title.textContent = `Node: ${p.nodeId}\nHash: ${p.hash.toString()}`;
            dot.appendChild(title);

            svg.appendChild(dot);
        });

        // Center cluster text
        const textCenterVal = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        textCenterVal.setAttribute('x', '200');
        textCenterVal.setAttribute('y', '195');
        textCenterVal.setAttribute('text-anchor', 'middle');
        textCenterVal.setAttribute('fill', '#f1f5f9');
        textCenterVal.setAttribute('font-family', 'Outfit');
        textCenterVal.setAttribute('font-size', '20px');
        textCenterVal.setAttribute('font-weight', '700');
        textCenterVal.textContent = ringNodes.length.toString();
        svg.appendChild(textCenterVal);

        const textCenterLbl = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        textCenterLbl.setAttribute('x', '200');
        textCenterLbl.setAttribute('y', '215');
        textCenterLbl.setAttribute('text-anchor', 'middle');
        textCenterLbl.setAttribute('fill', '#94a3b8');
        textCenterLbl.setAttribute('font-family', 'Plus Jakarta Sans');
        textCenterLbl.setAttribute('font-size', '10px');
        textCenterLbl.setAttribute('font-weight', '600');
        textCenterLbl.setAttribute('letter-spacing', '1px');
        textCenterLbl.textContent = 'PHYSICAL NODES';
        svg.appendChild(textCenterLbl);

        // Render highlighted lookup placement dot if set
        if (highlightedAngle !== null) {
            const hX = 200 + 120 * Math.cos(highlightedAngle);
            const hY = 200 + 120 * Math.sin(highlightedAngle);

            // Draw line to pointer
            const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
            line.setAttribute('x1', '200');
            line.setAttribute('y1', '200');
            line.setAttribute('x2', hX.toString());
            line.setAttribute('y2', hY.toString());
            line.setAttribute('stroke', '#a855f7');
            line.setAttribute('stroke-width', '2');
            line.setAttribute('stroke-dasharray', '4');
            svg.appendChild(line);

            // Glowing outer ring dot
            const glowDot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
            glowDot.setAttribute('cx', hX.toString());
            glowDot.setAttribute('cy', hY.toString());
            glowDot.setAttribute('r', '9');
            glowDot.setAttribute('fill', 'rgba(168, 85, 247, 0.4)');
            svg.appendChild(glowDot);

            const highlightDot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
            highlightDot.setAttribute('cx', hX.toString());
            highlightDot.setAttribute('cy', hY.toString());
            highlightDot.setAttribute('r', '5');
            highlightDot.setAttribute('fill', '#a855f7');
            
            const title = document.createElementNS('http://www.w3.org/2000/svg', 'title');
            title.textContent = highlightedLabel;
            highlightDot.appendChild(title);

            svg.appendChild(highlightDot);
        }
    }

    // Hash lookup placement action
    document.getElementById('btn-ring-lookup').addEventListener('click', async () => {
        const bucket = document.getElementById('ring-lookup-bucket').value.trim();
        const key = document.getElementById('ring-lookup-key').value.trim();
        const resultBox = document.getElementById('ring-lookup-result');

        if (!bucket || !key) {
            showToast('Please enter both bucket and key', 'error');
            return;
        }

        try {
            const res = await fetch(`/console/cluster/placement?bucket=${bucket}&key=${key}`);
            if (!res.ok) throw new Error('Key placement lookup failed');
            const data = await res.json();

            // Compute local hash position using FNV1a64
            const pathStr = bucket + '/' + key;
            const hashVal = FNV1a64(pathStr);
            const ringLimit = 2n ** 64n;
            const angle = Number(hashVal * 1000000n / ringLimit) / 1000000.0 * 2 * Math.PI - Math.PI / 2;

            resultBox.style.display = 'block';
            resultBox.innerHTML = `
                <div style="display: flex; flex-direction: column; gap: 8px;">
                    <div style="display: flex; justify-content: space-between;">
                        <span style="color: var(--text-secondary); font-weight: 500;">FNV-1a 64-bit Hash</span>
                        <span style="font-family: monospace; font-size: 12px; color: var(--accent-color);">${hashVal.toString()}</span>
                    </div>
                    <div style="display: flex; justify-content: space-between;">
                        <span style="color: var(--text-secondary); font-weight: 500;">Assigned Node</span>
                        <span style="font-weight: 600; color: ${getNodeColor(data.node_id)}">${data.node_id}</span>
                    </div>
                    <div style="display: flex; justify-content: space-between;">
                        <span style="color: var(--text-secondary); font-weight: 500;">Node Address</span>
                        <span style="color: var(--text-primary);">${data.address || 'Unknown'}</span>
                    </div>
                </div>
            `;

            // Redraw ring with highlighted pointer
            const svg = document.getElementById('ring-svg');
            renderRing(svg, angle, `Key: ${pathStr}\nHash: ${hashVal.toString()}\nNode: ${data.node_id}`);
        } catch (err) {
            showToast(err.message, 'error');
        }
    });

    // --- OTEL Traces Browser Logic ---
    async function loadTraces() {
        const tbody = document.getElementById('trace-list-body');
        tbody.innerHTML = '<tr><td colspan="6"><div class="loading-spinner"><i class="fa-solid fa-circle-notch fa-spin"></i></div></td></tr>';
        
        try {
            const res = await fetch('/console/traces');
            if (!res.ok) throw new Error('Failed to load traces');
            const spans = await res.json();

            tbody.innerHTML = '';
            if (spans.length === 0) {
                tbody.innerHTML = '<tr><td colspan="6" style="text-align: center; padding: 20px; color: var(--text-muted);">No trace spans captured in buffer.</td></tr>';
                return;
            }

            // Show newest traces first
            spans.reverse().forEach(span => {
                const durationMs = (span.Duration / 1000000.0).toFixed(2);
                const statusClass = span.Status < 400 ? 'status-badge enabled' : 'status-badge';
                const statusText = span.Status === 0 ? 'OK' : span.Status.toString();

                tbody.innerHTML += `
                    <tr>
                        <td style="font-weight: 600; color: var(--text-primary);">${span.Name}</td>
                        <td style="font-family: monospace; font-size: 12px; color: var(--text-muted);">${span.TraceID}</td>
                        <td style="font-family: monospace; font-size: 12px; color: var(--text-muted);">${span.SpanID}</td>
                        <td><span class="badge-version" style="color: var(--text-primary); text-transform: uppercase;">${span.Kind}</span></td>
                        <td style="font-weight: 500; color: var(--accent-color);">${durationMs} ms</td>
                        <td><span class="${statusClass}">${statusText}</span></td>
                    </tr>
                `;
            });
        } catch (err) {
            tbody.innerHTML = `<tr><td colspan="6" style="color: var(--danger-color); text-align: center; padding: 20px;">Error loading traces: ${err.message}</td></tr>`;
        }
    }

    document.getElementById('btn-refresh-traces').addEventListener('click', loadTraces);

    // --- Access Control / Policy Editor Logic ---
    let defaultUsers = ['admin', 'developer-john', 'anonymous'];
    let selectedUser = '';

    function getStorageUsers() {
        const stored = localStorage.getItem('console_users');
        if (stored) {
            try {
                return JSON.parse(stored);
            } catch (e) {
                return defaultUsers;
            }
        }
        localStorage.setItem('console_users', JSON.stringify(defaultUsers));
        return defaultUsers;
    }

    function saveStorageUser(username) {
        const users = getStorageUsers();
        if (!users.includes(username)) {
            users.push(username);
            localStorage.setItem('console_users', JSON.stringify(users));
        }
    }

    function loadPoliciesView() {
        const users = getStorageUsers();
        const listContainer = document.getElementById('policy-users-list');
        listContainer.innerHTML = '';

        users.forEach(u => {
            const item = document.createElement('div');
            item.className = `user-item ${selectedUser === u ? 'active' : ''}`;
            item.style.padding = '10px 12px';
            item.style.borderRadius = '8px';
            item.style.cursor = 'pointer';
            item.style.color = 'var(--text-secondary)';
            item.style.marginBottom = '4px';
            item.style.transition = 'var(--transition-smooth)';
            item.style.fontWeight = '500';
            
            if (selectedUser === u) {
                item.style.backgroundColor = 'rgba(99, 102, 241, 0.15)';
                item.style.color = 'var(--text-primary)';
                item.style.borderLeft = '3px solid var(--primary-color)';
            }

            item.innerHTML = `<i class="fa-solid fa-user" style="margin-right: 8px;"></i> ${u}`;
            item.addEventListener('click', () => selectUserPolicy(u));
            listContainer.appendChild(item);
        });
    }

    async function selectUserPolicy(username) {
        selectedUser = username;
        loadPoliciesView();

        const titleEl = document.getElementById('policy-current-user');
        const jsonArea = document.getElementById('policy-json-area');
        const btnSave = document.getElementById('btn-save-policy');
        const btnDelete = document.getElementById('btn-delete-policy');

        titleEl.textContent = username;
        jsonArea.value = '';
        btnSave.disabled = true;
        btnDelete.disabled = true;

        try {
            const res = await fetch(`/console/users/${username}/policy`);
            if (res.status === 404) {
                jsonArea.value = `{
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:*"],
      "Resource": ["arn:aws:s3:::*"]
    }
  ]
}`;
            } else if (!res.ok) {
                throw new Error('Failed to fetch user policy');
            } else {
                const data = await res.json();
                jsonArea.value = JSON.stringify(data, null, 2);
            }
            btnSave.disabled = false;
            btnDelete.disabled = false;
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    document.getElementById('btn-select-user').addEventListener('click', () => {
        const username = document.getElementById('new-user-name').value.trim();
        if (!username) {
            showToast('Username cannot be empty', 'error');
            return;
        }
        saveStorageUser(username);
        document.getElementById('new-user-name').value = '';
        selectUserPolicy(username);
    });

    document.getElementById('btn-save-policy').addEventListener('click', async () => {
        if (!selectedUser) return;
        const jsonArea = document.getElementById('policy-json-area');
        let rawJson = jsonArea.value.trim();

        try {
            // Basic JSON validate
            JSON.parse(rawJson);
        } catch (e) {
            showToast('Invalid JSON format: ' + e.message, 'error');
            return;
        }

        try {
            const res = await fetch(`/console/users/${selectedUser}/policy`, {
                method: 'PUT',
                body: rawJson,
                headers: {
                    'Content-Type': 'application/json'
                }
            });
            if (!res.ok) {
                const text = await res.text();
                throw new Error(text || 'Failed to save policy');
            }
            showToast(`Policy for "${selectedUser}" saved successfully!`, 'success');
        } catch (err) {
            showToast(err.message, 'error');
        }
    });

    document.getElementById('btn-delete-policy').addEventListener('click', async () => {
        if (!selectedUser) return;
        if (!confirm(`Are you sure you want to delete policy for user "${selectedUser}"?`)) return;

        try {
            const res = await fetch(`/console/users/${selectedUser}/policy`, {
                method: 'DELETE'
            });
            if (!res.ok) throw new Error('Failed to delete policy');
            showToast(`Policy for "${selectedUser}" deleted`, 'success');
            selectUserPolicy(selectedUser);
        } catch (err) {
            showToast(err.message, 'error');
        }
    });

    // --- Modal Control Bindings ---
    document.getElementById('btn-create-bucket').addEventListener('click', () => {
        modalCreateBucket.classList.add('show');
    });
    document.getElementById('btn-create-bucket-hero').addEventListener('click', () => {
        modalCreateBucket.classList.add('show');
    });
    document.getElementById('close-create-modal').addEventListener('click', () => {
        modalCreateBucket.classList.remove('show');
    });
    document.getElementById('btn-cancel-create').addEventListener('click', () => {
        modalCreateBucket.classList.remove('show');
    });
    document.getElementById('btn-submit-create').addEventListener('click', () => {
        const name = document.getElementById('new-bucket-name').value.trim();
        createBucket(name);
    });

    document.getElementById('close-versions-modal').addEventListener('click', () => {
        modalVersions.classList.remove('show');
    });

    document.getElementById('btn-delete-bucket').addEventListener('click', deleteCurrentBucket);
    document.getElementById('btn-refresh-objects').addEventListener('click', loadObjects);

    versioningToggle.addEventListener('change', (e) => {
        setVersioning(e.target.checked);
    });

    searchInput.addEventListener('input', (e) => {
        const query = e.target.value.toLowerCase();
        const rows = objectListBody.querySelectorAll('tr');
        rows.forEach(row => {
            const nameCell = row.querySelector('.file-name-cell span');
            if (nameCell) {
                const name = nameCell.textContent.toLowerCase();
                row.style.display = name.includes(query) ? '' : 'none';
            }
        });
    });

    document.getElementById('btn-logout').addEventListener('click', () => {
        localStorage.removeItem('token');
        window.location.href = '/console/logout';
    });

    // --- Initialization ---
    initCharts();
    loadBuckets();

    // Start background pollers (metrics every 2s, cluster membership every 5s)
    pollMetrics();
    pollClusterStatus();
    setInterval(pollMetrics, 2000);
    setInterval(pollClusterStatus, 5000);
});
