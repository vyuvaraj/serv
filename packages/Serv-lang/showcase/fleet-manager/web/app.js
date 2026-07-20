document.addEventListener('DOMContentLoaded', () => {
    const deviceForm = document.getElementById('device-form');
    const firmwareForm = document.getElementById('firmware-form');
    const devicesTbody = document.getElementById('devices-tbody');
    const statTotalDevices = document.getElementById('stat-total-devices');
    const statOnlineDevices = document.getElementById('stat-online-devices');
    const statTelemetryPoints = document.getElementById('stat-telemetry-points');
    const simulateSelect = document.getElementById('simulate-device-select');
    const btnSimulate = document.getElementById('btn-simulate-ingest');
    const flowBadge = document.getElementById('flow-event-label');

    // Pipeline elements
    const nodeDevice = document.getElementById('node-device');
    const nodeMesh = document.getElementById('node-mesh');
    const nodeBroker = document.getElementById('node-broker');
    const nodeWorker = document.getElementById('node-worker');
    const lineDeviceMesh = document.getElementById('line-device-mesh');
    const lineMeshBroker = document.getElementById('line-mesh-broker');
    const lineBrokerWorker = document.getElementById('line-broker-worker');
    const lineWorkerStore = document.getElementById('line-worker-store');

    function animatePipeline(step) {
        nodeDevice.classList.remove('active');
        nodeMesh.classList.remove('active');
        nodeBroker.classList.remove('active');
        nodeWorker.classList.remove('active');
        lineDeviceMesh.classList.remove('active');
        lineMeshBroker.classList.remove('active');
        lineBrokerWorker.classList.remove('active');
        lineWorkerStore.classList.remove('active');

        if (step === 'device') {
            nodeDevice.classList.add('active');
            flowBadge.textContent = 'Device Sending Packet...';
            flowBadge.style.color = '#fff';
        } else if (step === 'mesh') {
            nodeDevice.classList.add('active');
            nodeMesh.classList.add('active');
            lineDeviceMesh.classList.add('active');
            flowBadge.textContent = 'ServMesh Routing to Endpoint';
            flowBadge.style.color = '#4f46e5';
        } else if (step === 'broker') {
            nodeMesh.classList.add('active');
            nodeBroker.classList.add('active');
            lineMeshBroker.classList.add('active');
            flowBadge.textContent = 'Queue Ingest: telemetry.ingest';
            flowBadge.style.color = '#06b6d4';
        } else if (step === 'worker') {
            nodeBroker.classList.add('active');
            nodeWorker.classList.add('active');
            lineBrokerWorker.classList.add('active');
            flowBadge.textContent = 'Telemetry Worker Processing';
            flowBadge.style.color = '#10b981';
        } else if (step === 'store') {
            nodeWorker.classList.add('active');
            lineWorkerStore.classList.add('active');
            flowBadge.textContent = 'Telemetry Saved to DB / Cache';
            flowBadge.style.color = '#10b981';
        } else {
            flowBadge.textContent = 'Pipeline Monitor Active';
            flowBadge.style.color = '#9aa0a6';
        }
    }

    async function fetchStats() {
        try {
            const res = await fetch('/api/fleet-stats');
            if (res.ok) {
                const stats = await res.json();
                statTotalDevices.textContent = stats.total_devices;
                statOnlineDevices.textContent = stats.online_devices;
                statTelemetryPoints.textContent = stats.total_telemetry_points;
            }
        } catch (err) {
            console.error('Stats fetch error:', err);
        }
    }

    async function fetchDevices() {
        try {
            const res = await fetch('/api/devices');
            if (res.ok) {
                const data = await res.json();
                renderDevices(data.devices || []);
            }
        } catch (err) {
            console.error('Devices fetch error:', err);
        }
    }

    function renderDevices(devices) {
        if (!devices || devices.length === 0) {
            devicesTbody.innerHTML = `<tr><td colspan="5" class="loading-placeholder">No active nodes registered.</td></tr>`;
            simulateSelect.innerHTML = `<option value="">-- No Devices Online --</option>`;
            return;
        }

        devicesTbody.innerHTML = devices.map(dev => `
            <tr>
                <td>#${dev.id}</td>
                <td><strong>${escapeHtml(dev.name)}</strong></td>
                <td>${escapeHtml(dev.type)}</td>
                <td><span class="status-badge status-${dev.status}">${dev.status}</span></td>
                <td>v${dev.firmware_version}</td>
            </tr>
        `).join('');

        // Populate simulation select
        const currentVal = simulateSelect.value;
        simulateSelect.innerHTML = devices.map(d => `<option value="${d.id}">${d.name} (${d.id})</option>`).join('');
        if (currentVal) {
            simulateSelect.value = currentVal;
        }
    }

    function escapeHtml(str) {
        if (!str) return '';
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    deviceForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        const id = document.getElementById('device-id').value.trim();
        const name = document.getElementById('device-name').value.trim();
        const type = document.getElementById('device-type').value;

        try {
            const res = await fetch('/api/devices', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id, name, type })
            });
            if (res.ok) {
                deviceForm.reset();
                fetchStats();
                fetchDevices();
            }
        } catch (err) {
            console.error('Register device error:', err);
        }
    });

    firmwareForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        const version = document.getElementById('firmware-version').value.trim();

        try {
            const res = await fetch('/api/firmware/rollout', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ version })
            });
            if (res.ok) {
                alert(`Firmware rollout v${version} started successfully!`);
                fetchDevices();
            }
        } catch (err) {
            console.error('Firmware rollout error:', err);
        }
    });

    // Simulated ingestion pipeline
    btnSimulate.addEventListener('click', async () => {
        const deviceId = simulateSelect.value;
        if (!deviceId) {
            alert('Please register and select a device first!');
            return;
        }

        const metrics = ['temperature', 'vibration', 'humidity'];
        const metric = metrics[Math.floor(Math.random() * metrics.length)];
        const value = metric === 'temperature' ? (20 + Math.random() * 70) : (Math.random() * 15);

        // Run full visual telemetry pipeline
        animatePipeline('device');
        setTimeout(() => {
            animatePipeline('mesh');
            setTimeout(() => {
                animatePipeline('broker');
                setTimeout(() => {
                    animatePipeline('worker');
                    // In a real system, the telemetry would be published onto NATS/Kafka
                    // Here we trigger the local mock pub/sub handler
                    triggerMockIngest(deviceId, metric, value);
                    setTimeout(() => {
                        animatePipeline('store');
                        setTimeout(() => {
                            animatePipeline('idle');
                            fetchStats();
                            fetchDevices();
                        }, 800);
                    }, 800);
                }, 800);
            }, 800);
        }, 800);
    });

    // Fallback simulation triggers via polling or directly in JS
    function triggerMockIngest(deviceId, metric, value) {
        // Emit in background
        logEventStream(`Ingested telemetry: ${metric}=${value.toFixed(2)} from ${deviceId}`);
    }

    function logEventStream(msg) {
        console.log(`[Event Flow] ${msg}`);
    }

    // Auto update
    fetchStats();
    fetchDevices();
    setInterval(fetchStats, 3000);
    setInterval(fetchDevices, 3000);
});
