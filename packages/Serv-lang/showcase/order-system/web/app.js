document.addEventListener('DOMContentLoaded', () => {
    const orderForm = document.getElementById('order-form');
    const ordersTbody = document.getElementById('orders-tbody');
    const statRevenue = document.getElementById('stat-revenue');
    const statTotal = document.getElementById('stat-total');
    const statPending = document.getElementById('stat-pending');
    const btnRefresh = document.getElementById('btn-refresh');
    const flowBadge = document.getElementById('flow-event-label');

    // Node & line elements for flow rendering
    const nodeApi = document.getElementById('node-api');
    const nodeBroker = document.getElementById('node-broker');
    const nodeWorker = document.getElementById('node-worker');
    const lineApiBroker = document.getElementById('line-api-broker');
    const lineBrokerWorker = document.getElementById('line-broker-worker');

    function animatePipeline(step) {
        // Reset styles
        nodeApi.classList.remove('active');
        nodeBroker.classList.remove('active');
        nodeWorker.classList.remove('active');
        lineApiBroker.classList.remove('active');
        lineBrokerWorker.classList.remove('active');

        if (step === 'api') {
            nodeApi.classList.add('active');
            flowBadge.textContent = 'API Received Request';
            flowBadge.style.color = '#4f46e5';
        } else if (step === 'broker') {
            nodeApi.classList.add('active');
            nodeBroker.classList.add('active');
            lineApiBroker.classList.add('active');
            flowBadge.textContent = 'Event Published: orders.new';
            flowBadge.style.color = '#06b6d4';
        } else if (step === 'worker') {
            nodeBroker.classList.add('active');
            nodeWorker.classList.add('active');
            lineBrokerWorker.classList.add('active');
            flowBadge.textContent = 'Worker Processing Event';
            flowBadge.style.color = '#10b981';
        } else {
            flowBadge.textContent = 'Pipeline Idle';
            flowBadge.style.color = '#9aa0a6';
        }
    }

    async function fetchStats() {
        try {
            const res = await fetch('/api/dashboard');
            if (res.ok) {
                const stats = await res.json();
                statRevenue.textContent = `$${parseFloat(stats.total_revenue).toFixed(2)}`;
                statTotal.textContent = stats.total_orders;
                statPending.textContent = stats.pending_orders;
            }
        } catch (err) {
            console.error('Failed to fetch dashboard stats:', err);
        }
    }

    async function fetchOrders() {
        try {
            const res = await fetch('/api/orders?limit=10');
            if (res.ok) {
                const data = await res.json();
                renderOrders(data.orders || []);
            }
        } catch (err) {
            console.error('Failed to fetch orders:', err);
        }
    }

    function renderOrders(orders) {
        if (!orders || orders.length === 0) {
            ordersTbody.innerHTML = `<tr><td colspan="7" class="loading-placeholder">No orders found. Use the form above to place one.</td></tr>`;
            return;
        }

        ordersTbody.innerHTML = orders.map(order => {
            const dateStr = order.created_at ? new Date(order.created_at).toLocaleTimeString() : 'N/A';
            return `
                <tr>
                    <td>#${order.id}</td>
                    <td><strong>${escapeHtml(order.customer)}</strong></td>
                    <td>${escapeHtml(order.item)}</td>
                    <td>${order.quantity}</td>
                    <td>$${parseFloat(order.total).toFixed(2)}</td>
                    <td><span class="status-badge status-${order.status}">${order.status}</span></td>
                    <td>${dateStr}</td>
                </tr>
            `;
        }).join('');
    }

    function escapeHtml(str) {
        if (!str) return '';
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    orderForm.addEventListener('submit', async (e) => {
        e.preventDefault();

        const customer = document.getElementById('customer').value.trim();
        const item = document.getElementById('item').value;
        const quantity = parseInt(document.getElementById('quantity').value, 10);

        if (!customer) return;

        // Start step-by-step visual animation pipeline
        animatePipeline('api');

        try {
            const res = await fetch('/api/orders', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ customer, item, quantity })
            });

            if (res.ok) {
                setTimeout(() => {
                    animatePipeline('broker');
                    setTimeout(() => {
                        animatePipeline('worker');
                        setTimeout(() => {
                            animatePipeline('idle');
                            fetchStats();
                            fetchOrders();
                        }, 1200);
                    }, 1000);
                }, 800);
                orderForm.reset();
                document.getElementById('quantity').value = "1";
            } else {
                animatePipeline('idle');
                alert('Failed to place order.');
            }
        } catch (err) {
            animatePipeline('idle');
            console.error('Submit order error:', err);
        }
    });

    btnRefresh.addEventListener('click', () => {
        fetchStats();
        fetchOrders();
    });

    // Auto-refresh loops
    fetchStats();
    fetchOrders();
    setInterval(fetchStats, 3000);
    setInterval(fetchOrders, 3000);
});
