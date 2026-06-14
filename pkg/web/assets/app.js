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
        // Fallback for namespaces (e.g. s3:Name)
        const allNodes = parent.querySelectorAll('*');
        for (let node of allNodes) {
            if (node.localName === tagName) {
                return node.textContent;
            }
        }
        return defaultValue;
    }

    // Fetch and list buckets
    async function loadBuckets() {
        bucketListEl.innerHTML = '<div class="loading-spinner"><i class="fa-solid fa-circle-notch fa-spin"></i></div>';
        try {
            const res = await fetch('/');
            if (!res.ok) throw new Error('Failed to load buckets');
            const text = await res.text();
            
            const parser = new DOMParser();
            const xmlDoc = parser.parseFromString(text, 'text/xml');
            
            // Query selector looking for Bucket elements
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
                item.addEventListener('click', () => selectBucket(b.name, b.created));
                bucketListEl.appendChild(item);
            });
        } catch (err) {
            bucketListEl.innerHTML = `<div class="error-text" style="color: var(--danger-color); padding: 16px;">Error: ${err.message}</div>`;
        }
    }

    // Select Bucket
    async function selectBucket(name, createdTime) {
        currentBucket = name;
        
        // Update breadcrumb & header
        document.getElementById('breadcrumb-bucket').textContent = name;
        document.getElementById('breadcrumb-bucket').classList.remove('active');
        document.getElementById('breadcrumb-separator').style.display = 'inline';
        document.getElementById('breadcrumb-path').textContent = 'Objects';
        document.getElementById('breadcrumb-path').classList.add('active');

        currentBucketNameEl.textContent = name;
        if (createdTime) {
            const date = new Date(createdTime);
            bucketCreatedTimeEl.textContent = `Created: ${date.toLocaleString()}`;
        } else {
            bucketCreatedTimeEl.textContent = '';
        }

        // Highlight in sidebar
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

        // Load Versioning Status and Load Objects
        await loadVersioningStatus();
        await loadObjects();
    }

    // Load Versioning Status
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

    // Set Versioning Status
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
            versioningToggle.checked = !enabled; // Reset toggle
        }
    }

    // Load Objects in Bucket
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

            // Bind actions
            tr.querySelector('.btn-versions').addEventListener('click', () => openVersionsModal(obj.key));
            tr.querySelector('.btn-delete-obj').addEventListener('click', () => deleteObject(obj.key));

            objectListBody.appendChild(tr);
        });
    }

    // Delete Object
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

    // Create Bucket
    async function createBucket(name) {
        // Validation: lowercase, alphanumeric and hyphens, 3-63 chars
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

    // Delete Bucket
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
            document.getElementById('breadcrumb-bucket').textContent = 'Select a bucket';
            document.getElementById('breadcrumb-separator').style.display = 'none';
            document.getElementById('breadcrumb-path').textContent = '';
            await loadBuckets();
        } catch (err) {
            showToast(err.message, 'error');
        }
    }

    // Upload file logic
    function performUpload(file) {
        uploadProgressContainer.style.display = 'block';
        uploadFilenameEl.textContent = file.name;
        uploadPercentageEl.textContent = '0%';
        uploadProgressBar.style.width = '0%';

        const xhr = new XMLHttpRequest();
        xhr.open('PUT', `/${currentBucket}/${file.name}`, true);

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

    // Drag & Drop event bindings
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

    // Version history modal logic
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
            
            // Read version tags
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

            // Read delete markers
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

            // Sort versions by lastModified desc (newest first)
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

            // Badges
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

    // Modal Control Bindings
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

    // Object search filter
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

    // Logout functionality
    document.getElementById('btn-logout').addEventListener('click', () => {
        localStorage.removeItem('token');
        window.location.href = '/console/logout';
    });

    // Initial load
    loadBuckets();
});
