// CronPlus Web UI — Single Page Application

const API_BASE = '';
const OFFLINE_CACHE_KEY = 'cronplus_offline_cache_v2';
const OFFLINE_CACHE_KEYS_TO_CLEAR = ['cronplus_offline_cache_v1'];
const OFFLINE_CACHE_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;
let authToken = localStorage.getItem('cronplus_token') || '';
let sseConnection = null;
let currentPage = 'dashboard';
let appState = { status: null, tasks: [], taskDetails: {}, taskDetailLoading: {}, taskDetailErrors: {}, taskChecks: {}, runDetails: {}, runHistories: {}, deliveries: [], commands: [], connected: true };
let appStateSignatures = { status: '', tasks: '', deliveries: '', commands: '' };
let refreshInFlight = false;
let refreshQueued = false;
let refreshQueuedForceRender = false;
let deliveryPreviewText = '';

// ===== Auth =====

async function init() {
    // Step 1: Try localStorage token
    if (authToken) {
        const ok = await testToken(authToken);
        if (ok) { showApp(); return; }
    }

    // Step 2: Try auto-auth (localhost-only)
    try {
        const resp = await fetch(`${API_BASE}/api/auth/check`);
        if (resp.ok) {
            const data = await resp.json();
            if (data.token) {
                authToken = data.token;
                localStorage.setItem('cronplus_token', authToken);
                showApp();
                return;
            }
        }
    } catch (e) { /* not localhost or server down */ }

    // Step 3: Show login
    showLogin();
}

async function testToken(token) {
    try {
        const resp = await fetch(`${API_BASE}/api/status`, {
            headers: { 'Authorization': `Bearer ${token}` }
        });
        return resp.ok;
    } catch { return false; }
}

function showLogin() {
    document.getElementById('login-screen').style.display = 'block';
    document.getElementById('main-app').style.display = 'none';
    document.getElementById('token-input').focus();
}

async function attemptLogin() {
    const input = document.getElementById('token-input');
    const token = input.value.trim();
    if (!token) return;

    const ok = await testToken(token);
    if (ok) {
        authToken = token;
        localStorage.setItem('cronplus_token', authToken);
        showApp();
    } else {
        input.style.borderColor = 'var(--danger)';
        setTimeout(() => input.style.borderColor = '', 2000);
    }
}

function showApp() {
    document.getElementById('login-screen').style.display = 'none';
    document.getElementById('main-app').style.display = 'flex';
    loadOfflineCache();
    connectSSE();
    navigate(window.location.hash || '#/');
    refreshAll();
}

// ===== API Helpers =====

async function api(method, path, body) {
    try {
        const opts = {
            method,
            headers: {
                'Authorization': `Bearer ${authToken}`,
                'Content-Type': 'application/json'
            }
        };
        if (body) opts.body = JSON.stringify(body);
        const resp = await fetch(`${API_BASE}${path}`, opts);
        if (resp.status === 401) { showLogin(); return null; }

        const text = await resp.text();
        let data = {};
        if (text) {
            try {
                data = JSON.parse(text);
            } catch {
                data = { message: text };
            }
        }
        if (!resp.ok) {
            return {
                error: data.error || `http_${resp.status}`,
                message: data.message || resp.statusText || 'Request failed'
            };
        }
        return data;
    } catch (err) {
        setConnectionState(false);
        const rawMessage = err?.message || '';
        return {
            error: 'network_error',
            message: rawMessage === 'Failed to fetch' ? 'CronPlus daemon is not reachable.' : rawMessage || 'Could not reach CronPlus'
        };
    }
}

async function refreshAll(options = {}) {
    if (refreshInFlight) {
        refreshQueued = true;
        refreshQueuedForceRender = refreshQueuedForceRender || !!options.forceRender;
        return;
    }

    refreshInFlight = true;
    try {
        await performRefreshAll(options);
    } finally {
        refreshInFlight = false;
        if (refreshQueued) {
            const queuedOptions = { forceRender: refreshQueuedForceRender };
            refreshQueued = false;
            refreshQueuedForceRender = false;
            refreshAll(queuedOptions);
        }
    }
}

async function performRefreshAll(options = {}) {
    const [status, tasks, deliveries, commands] = await Promise.all([
        api('GET', '/api/status'),
        api('GET', '/api/tasks'),
        api('GET', '/api/deliveries'),
        api('GET', '/api/commands')
    ]);

    let changed = !!options.forceRender;
    const changes = {
        status: !!options.forceRender,
        tasks: !!options.forceRender,
        deliveries: !!options.forceRender,
        commands: !!options.forceRender
    };
    if (status && !status.error) {
        const sig = stateSignature(status);
        if (sig !== appStateSignatures.status) {
            appState.status = status;
            appStateSignatures.status = sig;
            changed = true;
            changes.status = true;
            saveOfflineCache();
        }
    }
    if (tasks && !tasks.error) {
        const nextTasks = tasks.tasks || [];
        const sig = stateSignature(nextTasks);
        if (sig !== appStateSignatures.tasks) {
            appState.tasks = nextTasks;
            const taskIDs = new Set(nextTasks.map(t => t.id));
            appState.taskDetails = Object.fromEntries(Object.entries(appState.taskDetails).filter(([id]) => taskIDs.has(id)));
            appState.runHistories = Object.fromEntries(Object.entries(appState.runHistories).filter(([id]) => taskIDs.has(id)));
            appState.runDetails = Object.fromEntries(Object.entries(appState.runDetails).filter(([, run]) => taskIDs.has(run?.taskID)));
            appState.taskDetailErrors = {};
            appState.taskChecks = {};
            appStateSignatures.tasks = sig;
            changed = true;
            changes.tasks = true;
            saveOfflineCache();
        }
    }
    if (deliveries && !deliveries.error) {
        const nextDeliveries = deliveries.profiles || [];
        const sig = stateSignature(nextDeliveries);
        if (sig !== appStateSignatures.deliveries) {
            appState.deliveries = nextDeliveries;
            appStateSignatures.deliveries = sig;
            changed = true;
            changes.deliveries = true;
            saveOfflineCache();
        }
    }
    if (commands && !commands.error) {
        const nextCommands = commands.commands || [];
        const sig = stateSignature(nextCommands);
        if (sig !== appStateSignatures.commands) {
            appState.commands = nextCommands;
            appStateSignatures.commands = sig;
            changed = true;
            changes.commands = true;
            saveOfflineCache();
        }
    }
    if (changed && !hasActiveEditorState()) {
        updateCurrentPage(changes);
    }
}

function stateSignature(value) {
    return JSON.stringify(value || null);
}

// ===== SSE =====

let sseRetryDelay = 1000;
let sseReconnectTimer = null;
const SSE_MAX_RETRY = 30000;

function connectSSE() {
    if (sseReconnectTimer) {
        clearTimeout(sseReconnectTimer);
        sseReconnectTimer = null;
    }
    if (sseConnection) {
        sseConnection.onopen = null;
        sseConnection.onerror = null;
        sseConnection.close();
    }

    // SSE doesn't support custom headers, so we pass token as query param
    sseConnection = new EventSource(`${API_BASE}/api/events?token=${authToken}`);
    const connection = sseConnection;

    sseConnection.onopen = () => {
        if (sseConnection !== connection) return;
        sseRetryDelay = 1000;
        setConnectionState(true);
    };

    sseConnection.onerror = () => {
        if (sseConnection !== connection) return;
        setConnectionState(false);
        connection.close();
        sseConnection = null;
        if (document.getElementById('main-app').style.display === 'none') return;
        sseReconnectTimer = setTimeout(() => {
            sseReconnectTimer = null;
            sseRetryDelay = Math.min(sseRetryDelay * 2, SSE_MAX_RETRY);
            connectSSE();
        }, sseRetryDelay);
    };

    sseConnection.addEventListener('run_started', () => refreshAll());
    sseConnection.addEventListener('run_completed', () => refreshAll());
    sseConnection.addEventListener('task_updated', () => refreshAll());
    sseConnection.addEventListener('status', (e) => {
        try {
            const status = JSON.parse(e.data);
            const sig = stateSignature(status);
            if (sig !== appStateSignatures.status) {
                appState.status = status;
                appStateSignatures.status = sig;
                if (!hasActiveEditorState()) updateCurrentPage({ status: true });
            }
        } catch {}
    });
}

// ===== Router =====

function navigate(hash) {
    const path = hash.replace('#', '') || '/';
    if (path !== currentPage) {
        closeRouteModals();
    }
    currentPage = path;

    document.querySelectorAll('.nav-link').forEach(link => {
        const page = link.dataset.page;
        link.classList.toggle('active',
            (path === '/' && page === 'dashboard') ||
            path.startsWith('/' + page)
        );
    });

    renderCurrentPage();
}

window.addEventListener('hashchange', () => navigate(window.location.hash));

function goToHash(hash) {
    if (window.location.hash === hash) {
        navigate(hash);
        return;
    }
    window.location.hash = hash;
}

function closeRouteModals() {
    ['import-modal', 'edit-profile-modal', 'preview-modal'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.remove();
    });
}

document.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && document.getElementById('login-screen').style.display !== 'none') {
        attemptLogin();
    }
});

document.addEventListener('click', (e) => {
    if (!(e.target instanceof Element)) return;
    const actionButton = e.target.closest('[data-action]');
    if (actionButton) {
        switch (actionButton.dataset.action) {
            case 'retry-run-history':
                loadRunHistory(actionButton.dataset.taskId || '');
                break;
            case 'retry-run-detail':
                loadRunDetail(actionButton.dataset.taskId || '', actionButton.dataset.runId || '');
                break;
            case 'retry-refresh':
                refreshAll({ forceRender: true });
                break;
        }
        return;
    }

    const button = e.target.closest('[data-profile-action]');
    if (!button) return;

    const id = button.dataset.profileId || '';
    switch (button.dataset.profileAction) {
        case 'edit':
            showEditProfileModal(id);
            break;
        case 'toggle-commands':
            toggleProfileCommands(id, button.dataset.profileEnabled === 'true');
            break;
        case 'test':
            testProfile(id);
            break;
        case 'delete':
            deleteProfile(id);
            break;
    }
});

function renderCurrentPage() {
    const content = document.getElementById('content');
    const path = currentPage;

    if (path === '/' || path === '/dashboard') content.innerHTML = renderDashboard();
    else if (path === '/tasks') content.innerHTML = renderTaskList();
    else if (path.startsWith('/tasks/') && path.includes('/runs/')) {
        content.innerHTML = renderRunDetail(path);
        const parts = path.split('/');
        loadRunDetail(parts[2], parts[4]);
    }
    else if (path.startsWith('/tasks/')) {
        const taskID = path.split('/')[2];
        content.innerHTML = renderTaskDetail(path);
        loadTaskDetail(taskID);
        loadRunHistory(taskID);
    }
    else if (path === '/delivery') content.innerHTML = renderDelivery();
    else if (path === '/commands') content.innerHTML = renderCommands();
    else if (path === '/settings') content.innerHTML = renderSettings();
    else content.innerHTML = '<div class="empty-state"><h3>Page not found</h3></div>';
}

function updateCurrentPage(changes = {}) {
    const path = currentPage;

    if (path === '/' || path === '/dashboard') {
        const el = document.getElementById('dashboard-content');
        if (el) {
            el.innerHTML = renderDashboardContent();
            return;
        }
    } else if (path === '/tasks') {
        const el = document.getElementById('task-list-content');
        if (el) {
            el.innerHTML = renderTaskListContent();
            return;
        }
    } else if (path === '/delivery') {
        const el = document.getElementById('delivery-list-content');
        if (el) {
            el.innerHTML = renderDeliveryList();
            return;
        }
    } else if (path === '/commands') {
        const el = document.getElementById('commands-content');
        if (el) {
            el.innerHTML = renderCommandsContent();
            return;
        }
    } else if (path === '/settings') {
        if (changes.status) {
            renderCurrentPage();
            return;
        }
    } else if (path.startsWith('/tasks/') && !path.includes('/runs/')) {
        const taskID = path.split('/')[2];
        if (changes.tasks) {
            loadTaskDetail(taskID, { force: true });
            loadRunHistory(taskID);
            return;
        }
    }

    renderCurrentPage();
}

// ===== Dashboard =====

function renderDashboard() {
    return `
        <div class="page-header">
            <h1>Dashboard</h1>
            <p>Overview of your automation tasks</p>
        </div>
        <div id="dashboard-content">${renderDashboardContent()}</div>
    `;
}

function renderDashboardContent() {
    const s = appState.status || {};
    const t = s.tasks || {};
    return `
        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-label">Total Tasks</div>
                <div class="stat-value">${t.total || 0}</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Enabled</div>
                <div class="stat-value success">${t.enabled || 0}</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Disabled</div>
                <div class="stat-value">${t.disabled || 0}</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Recent Failures</div>
                <div class="stat-value ${(s.recentFailures||0) > 0 ? 'danger' : 'success'}">${s.recentFailures || 0}</div>
            </div>
        </div>
        ${renderAttentionItems(s.attentionItems || [])}
        ${s.nextRun ? `
        <div class="stat-card" style="margin-bottom:24px">
            <div class="stat-label">Next Scheduled Run</div>
            <p style="font-size:16px;margin-top:8px">
                <strong>${esc(s.nextRun.taskName)}</strong>
                &mdash; ${formatTime(s.nextRun.scheduledAt)}
            </p>
        </div>` : ''}
        <h2 style="font-size:18px;margin-bottom:16px">Recent Tasks</h2>
        ${renderTaskCards(appState.tasks.slice(0, 5))}
    `;
}

function renderAttentionItems(items) {
    if (!items.length) {
        return `
            <div class="attention-panel attention-clear">
                <div>
                    <h2>Needs Attention</h2>
                    <p>No failed runs, stale manifests, or broken delivery profiles are showing right now.</p>
                </div>
                <span class="badge badge-success">Clear</span>
            </div>
        `;
    }

    return `
        <div class="attention-panel">
            <div class="attention-header">
                <div>
                    <h2>Needs Attention</h2>
                    <p>${items.length} item${items.length === 1 ? '' : 's'} need review.</p>
                </div>
            </div>
            <div class="attention-list">
                ${items.map(item => `
                    <button class="attention-item" onclick="${attentionClickHandler(item)}">
                        <span class="attention-severity ${esc(item.severity || 'warning')}"></span>
                        <span class="attention-copy">
                            <strong>${esc(item.title || 'Needs review')}</strong>
                            <span>${item.taskName ? `${esc(item.taskName)} · ` : ''}${esc(item.detail || '')}</span>
                        </span>
                        <span class="attention-action">${esc(item.action || 'Open')}</span>
                    </button>
                `).join('')}
            </div>
        </div>
    `;
}

function attentionClickHandler(item) {
    let target = '#/tasks';
    if (item.runID) target = `#/tasks/${item.taskID}/runs/${item.runID}`;
    else if (item.kind === 'delivery') target = '#/delivery';
    else if (item.taskID) target = `#/tasks/${item.taskID}`;
    return `window.location.hash='${String(target).replace(/'/g, "\\'")}'`;
}

// ===== Tasks =====

function renderTaskList() {
    return `
        <div class="page-header" style="display:flex;justify-content:space-between;align-items:start">
            <div>
                <h1>Tasks</h1>
                <p>Manage your automation scripts</p>
            </div>
            <button class="btn btn-primary" onclick="promptImportTask()">+ Import Task</button>
        </div>
        <div id="task-list-content">${renderTaskListContent()}</div>
    `;
}

function renderTaskListContent() {
    return `
        ${appState.tasks.length === 0 ?
            '<div class="empty-state"><div class="icon">📋</div><h3>No tasks yet</h3><p>Import a task package to get started.</p></div>' :
            renderTaskCards(appState.tasks)
        }
    `;
}

function renderTaskCards(tasks) {
    return `<div class="task-list">${tasks.map(t => {
        const lr = t.lastRun;
        let statusClass = t.enabled ? 'success' : 'disabled';
        if (t.running) statusClass = 'running';
        else if (lr && lr.status !== 'success') statusClass = 'failed';

        return `
        <div class="task-row${t.running ? ' is-running' : ''}" role="link" tabindex="0" onclick="window.location.hash='#/tasks/${t.id}'" onkeydown="if(event.key==='Enter')window.location.hash='#/tasks/${t.id}'">
            <div class="task-status-dot ${statusClass}"></div>
            <div class="task-info">
                <div class="task-name">${esc(t.name)}</div>
                <div class="task-meta">
                    ${esc(t.scheduleSummary || 'No schedule')}
                    ${t.nextRun ? `· Next: ${formatTime(t.nextRun)}` : ''}
                    ${t.manifestStatus?.changed ? '· <span class="badge badge-warning">manifest changed</span>' : ''}
                    ${lr ? `· Last: <span class="badge badge-${runStatusBadge(lr.status)}">${esc(lr.status)}</span>` : ''}
                </div>
            </div>
            <div class="task-actions" onclick="event.preventDefault();event.stopPropagation()">
                <button class="btn btn-sm" onclick="toggleTask('${t.id}', ${!t.enabled})">${t.enabled ? 'Disable' : 'Enable'}</button>
                <button class="btn btn-sm btn-primary" onclick="runTask('${t.id}')">▶ Run</button>
            </div>
        </div>`;
    }).join('')}</div>`;
}

// ===== Task Detail =====

function renderTaskDetail(path) {
    const id = path.split('/')[2];
    const task = appState.taskDetails[id] || appState.tasks.find(t => t.id === id);
    if (!task) {
        if (appState.taskDetailErrors[id]) {
            return `<div class="empty-state"><h3>${esc(appState.taskDetailErrors[id])}</h3></div>`;
        }
        return '<div class="empty-state"><h3>Loading task...</h3></div>';
    }

    return `
        <div class="detail-header">
            <a href="#/tasks" class="back-link" onclick="goToHash('#/tasks');return false;">←</a>
            <h1>${esc(task.name)}</h1>
            <span class="badge badge-${task.enabled ? 'success' : 'muted'}">${task.enabled ? 'Enabled' : 'Disabled'}</span>
            ${task.manifestStatus?.changed ? '<span class="badge badge-warning">Manifest Changed</span>' : ''}
            <div class="detail-actions">
                <button class="btn btn-primary" onclick="runTask('${id}')">▶ Run Now</button>
                <button class="btn" onclick="checkImportedTask('${id}')">Check</button>
                <button class="btn" onclick="reloadTask('${id}')">Reload Manifest</button>
                <button class="btn" onclick="previewDelivery('${id}')">Preview Delivery</button>
                <button class="btn" onclick="toggleTask('${id}', ${!task.enabled})">${task.enabled ? 'Disable' : 'Enable'}</button>
                <button class="btn" style="color:var(--danger);border-color:var(--danger);" onclick="removeTaskImport('${id}')">Remove Import</button>
            </div>
        </div>
        <div class="task-content-layout">
            <div class="task-main">
                <div class="detail-card" style="margin-bottom:32px;">
                    <h3>Description</h3>
                    <p style="font-size:15px;line-height:1.6;color:var(--text-primary)">${esc(task.description || 'No description provided.')}</p>
                </div>
                <div id="task-check-result-${id}">
                    ${appState.taskChecks[id] ? renderTaskPackageCheck(appState.taskChecks[id]) : ''}
                </div>
                
                <h2 style="font-size:18px;margin-bottom:16px;">Run History</h2>
                <div id="run-history-${id}">${renderRunHistoryInitial(id)}</div>
            </div>

            <div class="task-sidebar">
                <div class="detail-card">
                    <h3>Schedule</h3>
                    <div style="font-family:var(--font-mono);font-size:18px;color:var(--accent);margin-bottom:8px;font-weight:600;letter-spacing:1px">${esc(task.scheduleSummary || 'N/A')}</div>
                    ${task.nextRun ? `<p style="color:var(--text-secondary);font-size:13px;display:flex;align-items:center;gap:6px;"><span class="task-status-dot running"></span> Next: ${formatTime(task.nextRun)}</p>` : ''}
                    ${renderNextRuns(task.nextRuns || [])}
                </div>

                <div class="detail-card">
                    <h3>Manifest</h3>
                    <div class="manifest-row"><span class="label">Loaded</span><span class="value">${formatTime(task.manifestStatus?.lastReloadedAt)}</span></div>
                    <div class="manifest-row"><span class="label">Disk State</span><span class="value">${task.manifestStatus?.changed ? 'changed' : 'current'}</span></div>
                    ${task.manifestStatus?.currentModifiedAt ? `<div class="manifest-row"><span class="label">Modified</span><span class="value">${formatTime(task.manifestStatus.currentModifiedAt)}</span></div>` : ''}
                    ${task.manifestStatus?.error ? `<div class="delivery-error">${esc(task.manifestStatus.error)}</div>` : ''}
                </div>

                ${task.timeline ? `<div class="detail-card">
                    <h3>Timeline</h3>
                    <div class="manifest-row"><span class="label">Runs</span><span class="value">${task.timeline.totalRuns || 0}</span></div>
                    <div class="manifest-row"><span class="label">Last Run</span><span class="value">${formatTime(task.timeline.lastRunAt)}</span></div>
                    <div class="manifest-row"><span class="label">Last Success</span><span class="value">${formatTime(task.timeline.lastSuccessAt)}</span></div>
                    <div class="manifest-row"><span class="label">Last Failure</span><span class="value">${formatTime(task.timeline.lastFailureAt)}</span></div>
                    <div class="manifest-row"><span class="label">Avg Duration</span><span class="value">${formatDurationMs(task.timeline.averageDurationMs)}</span></div>
                    <div class="manifest-row"><span class="label">Failure Streak</span><span class="value">${task.timeline.consecutiveFailures || 0}</span></div>
                </div>` : ''}

	                ${task.manifest ? `<div class="detail-card">
	                    <h3>Runtime</h3>
	                    <div class="manifest-row"><span class="label">Strategy</span><span class="value">${esc(task.manifest.runtime?.environment?.strategy || 'system')}</span></div>
	                    <div class="manifest-row"><span class="label">Timeout</span><span class="value">${task.manifest.runtime?.timeoutSeconds || 120}s</span></div>
	                    <div class="manifest-row"><span class="label">Max Output</span><span class="value">${task.manifest.runtime?.maxOutputKB || 512}KB</span></div>
	                    <div class="manifest-row"><span class="label">Run Isolation</span><span class="value">${task.manifest.runtime?.isolatedRun === false ? 'off' : 'on'}</span></div>
	                    <div class="manifest-row"><span class="label">Kill Grace</span><span class="value">${task.manifest.runtime?.resourceLimits?.gracefulKillSeconds || 5}s</span></div>
	                    ${task.manifest.runtime?.resourceLimits?.maxOpenFiles ? `<div class="manifest-row"><span class="label">Open Files</span><span class="value">${task.manifest.runtime.resourceLimits.maxOpenFiles}</span></div>` : ''}
	                    ${task.manifest.runtime?.resourceLimits?.maxProcesses ? `<div class="manifest-row"><span class="label">Processes</span><span class="value">${task.manifest.runtime.resourceLimits.maxProcesses}</span></div>` : ''}
	                    ${task.manifest.runtime?.resourceLimits?.maxCPUSeconds ? `<div class="manifest-row"><span class="label">CPU Limit</span><span class="value">${task.manifest.runtime.resourceLimits.maxCPUSeconds}s</span></div>` : ''}
	                    ${task.manifest.runtime?.resourceLimits?.maxMemoryMB ? `<div class="manifest-row"><span class="label">Memory</span><span class="value">${task.manifest.runtime.resourceLimits.maxMemoryMB}MB</span></div>` : ''}
	                    ${(task.manifest.delivery?.profiles||[]).length > 0 ? `<div class="manifest-row"><span class="label">Delivery</span><span class="value">${task.manifest.delivery.profiles.length} profile(s)</span></div>` : ''}
	                    ${(task.manifest.delivery?.sendOn||[]).length > 0 ? `<div class="manifest-row"><span class="label">Send On</span><span class="value">${esc(task.manifest.delivery.sendOn.join(', '))}</span></div>` : ''}
	                </div>` : ''}
                
                <div class="detail-card">
                    <h3>Package Directory</h3>
                    <div style="background:rgba(0,0,0,0.3);padding:10px 14px;border-radius:6px;font-family:var(--font-mono);font-size:12px;word-break:break-all;border:1px solid var(--border);color:var(--text-secondary)">
                        ${esc(task.packageDir || 'N/A')}
                    </div>
                </div>
            </div>
        </div>
    `;
}

async function loadTaskDetail(taskID, options = {}) {
    if (!options.force && (appState.taskDetails[taskID] || appState.taskDetailLoading[taskID])) return;
    appState.taskDetailLoading[taskID] = true;
    const data = await api('GET', `/api/tasks/${taskID}`);
    delete appState.taskDetailLoading[taskID];
    if (!data || data.error) {
        const cached = appState.taskDetails[taskID] || appState.tasks.find(t => t.id === taskID);
        appState.taskDetailErrors[taskID] = data?.message || 'Task not found';
        if (!cached && currentPage === `/tasks/${taskID}`) renderCurrentPage();
        return;
    }
    delete appState.taskDetailErrors[taskID];
    appState.taskDetails[taskID] = data;
    saveOfflineCache();
    if (currentPage === `/tasks/${taskID}`) {
        renderCurrentPage();
    }
}

async function loadRunHistory(taskID) {
    const data = await api('GET', `/api/tasks/${taskID}/runs`);
    const el = document.getElementById(`run-history-${taskID}`);
    if (!el) return;
    if (!data) {
        el.innerHTML = renderRunHistoryUnavailable(taskID, 'CronPlus could not load this task’s runs. Retry after the daemon reconnects.');
        return;
    }
    if (data.error) {
        el.innerHTML = renderRunHistoryUnavailable(taskID, data.message || 'Could not load run history.');
        return;
    }

    const runs = data.runs || [];
    appState.runHistories[taskID] = runs;
    saveOfflineCache();
    if (runs.length === 0) {
        el.innerHTML = renderInlineState('No runs yet', 'This task has not recorded a run.', 'neutral');
        return;
    }

    el.innerHTML = renderRunHistoryList(taskID, runs);
}

function renderRunHistoryInitial(taskID) {
    const cached = appState.runHistories[taskID];
    if (Array.isArray(cached) && cached.length > 0) {
        return renderRunHistoryList(taskID, cached, {
            notice: appState.connected ? '' : 'Showing cached run history while CronPlus is disconnected.'
        });
    }
    if (Array.isArray(cached)) {
        return renderInlineState('No runs yet', 'This task has not recorded a run.', 'neutral');
    }
    return renderInlineState('Loading run history', '', 'neutral');
}

function renderRunHistoryUnavailable(taskID, message) {
    const cached = appState.runHistories[taskID];
    if (Array.isArray(cached) && cached.length > 0) {
        return renderRunHistoryList(taskID, cached, {
            notice: message || 'Showing cached run history while CronPlus is disconnected.',
            tone: 'warning'
        });
    }
    return renderInlineState(
        'Run history unavailable',
        message || 'CronPlus could not load this task’s runs.',
        'error',
        'Retry',
        `data-action="retry-run-history" data-task-id="${attr(taskID)}"`
    );
}

function renderRunHistoryList(taskID, runs, options = {}) {
    return `
        ${options.notice ? renderInlineNotice(options.notice, options.tone || 'warning') : ''}
        <div class="run-history-list">
            ${runs.map(r => {
                const status = runStatusFor(r);
                const delivery = deliveryHistorySummary(r.deliveryResults || []);
                return `<a class="run-history-row" href="#/tasks/${r.taskID || taskID}/runs/${r.id}">
                    <div class="run-history-primary">
                        <span class="run-history-status-group">
                            <span class="badge badge-${runStatusBadge(status)}">${esc(runHistoryStatusLabel(status))}</span>
                        </span>
                        ${renderDeliveryHistorySummary(delivery)}
                    </div>
                    <div class="run-history-meta">
                        <span><span class="run-history-label">Trigger</span>${esc(r.trigger)}</span>
                        <span><span class="run-history-label">Started</span>${formatTime(r.startedAt)}</span>
                        <span><span class="run-history-label">Duration</span>${r.outcome?.durationMs ? (r.outcome.durationMs / 1000).toFixed(1) + 's' : '—'}</span>
                    </div>
                    <span class="run-history-view">View</span>
                </a>`;
            }).join('')}
        </div>
    `;
}

// ===== Run Detail =====

function renderRunDetail(path) {
    const parts = path.split('/');
    const taskID = parts[2];
    const runID = parts[4];
    const task = appState.tasks.find(t => t.id === taskID);

    return `
        <div class="detail-header">
            <a href="#/tasks/${taskID}" class="back-link" aria-label="Back to task" onclick="goToHash('#/tasks/${taskID}');return false;">←</a>
            <span class="breadcrumb-label">${esc(task?.name || 'Task')}</span>
            <h1>Run Detail</h1>
        </div>
        <div id="run-detail-content">Loading...</div>
    `;
}

async function loadRunDetail(taskID, runID) {
    const run = await api('GET', `/api/tasks/${taskID}/runs/${runID}`);
    const el = document.getElementById('run-detail-content');
    if (!el || !run) return;
    if (run.error) {
        const cached = cachedRunDetail(taskID, runID);
        if (cached) {
            appState.runDetails[runID] = cached;
            el.innerHTML = renderRunDetailContent(cached, {
                notice: run.message || 'Showing cached run details while CronPlus is disconnected.',
                noticeTone: 'warning'
            });
            return;
        }
        el.innerHTML = renderInlineState(
            'Run detail unavailable',
            run.message || 'CronPlus could not load this run. Retry after the daemon reconnects.',
            'error',
            'Retry',
            `data-action="retry-run-detail" data-task-id="${attr(taskID)}" data-run-id="${attr(runID)}"`
        );
        return;
    }
    appState.runDetails[runID] = run;
    saveOfflineCache();

    el.innerHTML = renderRunDetailContent(run);
}

function cachedRunDetail(taskID, runID) {
    const detail = appState.runDetails[runID];
    if (detail && (!detail.taskID || detail.taskID === taskID)) {
        return detail;
    }
    const history = appState.runHistories[taskID];
    if (!Array.isArray(history)) return null;
    return history.find(r => r.id === runID) || null;
}

function renderRunDetailContent(run, options = {}) {
    const status = runStatusFor(run);
    const diagnostics = run.outcome?.diagnostics || {};
    const cleanup = diagnostics.cleanup || {};

    let deliveryHTML = '';
    if (run.deliveryResults && run.deliveryResults.length > 0) {
        deliveryHTML = `<div class="delivery-results">
            <h3>DELIVERY RESULTS</h3>
            ${run.deliveryResults.map(dr => `
                <div class="delivery-result-row">
                    <div>
                        <div class="profile-name">${esc(dr.profileName)}</div>
                        ${dr.error ? `<div class="delivery-error">${esc(dr.error)}</div>` : ''}
                    </div>
                    <span class="badge badge-${deliveryStatusBadge(dr.status)}">${esc(dr.status)}</span>
                </div>
            `).join('')}
        </div>`;
    }

    return `
        ${options.notice ? renderInlineNotice(options.notice, options.noticeTone || 'warning') : ''}
        <div class="detail-grid">
            <div class="detail-card">
                <h3>Status</h3>
                <p><span class="badge badge-${runStatusBadge(status)}">${esc(status)}</span></p>
            </div>
            <div class="detail-card">
                <h3>Trigger</h3>
                <p>${esc(run.trigger)}</p>
            </div>
            <div class="detail-card">
                <h3>Duration</h3>
                <p>${run.outcome?.durationMs ? (run.outcome.durationMs / 1000).toFixed(2) + 's' : '—'}</p>
            </div>
            <div class="detail-card">
                <h3>Exit Code</h3>
                <p style="font-family:var(--font-mono)">${run.outcome?.exitCode ?? '—'}</p>
            </div>
        </div>
        ${renderRunDiagnosis(run)}
        ${run.outcome?.diagnostics ? `
        <div class="detail-card" style="margin-bottom:16px">
            <h3>Run Diagnostics</h3>
            <div class="manifest-row"><span class="label">Python</span><span class="value">${esc(diagnostics.pythonExecutable || 'N/A')}</span></div>
            <div class="manifest-row"><span class="label">Script</span><span class="value">${esc(diagnostics.scriptPath || 'N/A')}</span></div>
            <div class="manifest-row"><span class="label">Working Dir</span><span class="value">${esc(diagnostics.workingDirectory || 'N/A')}</span></div>
            <div class="manifest-row"><span class="label">Env</span><span class="value">${esc(diagnostics.environmentStrategy || 'N/A')}</span></div>
            ${diagnostics.envFile ? `<div class="manifest-row"><span class="label">Env File</span><span class="value">${esc(diagnostics.envFile)}</span></div>` : ''}
            <div class="manifest-row"><span class="label">Timeout</span><span class="value">${diagnostics.timeoutSeconds || 0}s</span></div>
            <div class="manifest-row"><span class="label">Root PID</span><span class="value">${diagnostics.rootPID || '—'}</span></div>
            <div class="manifest-row"><span class="label">Process Group</span><span class="value">${diagnostics.processGroupID || '—'}</span></div>
            <div class="manifest-row"><span class="label">Run Isolation</span><span class="value">${diagnostics.isolatedRun ? 'on' : 'off'}</span></div>
            ${diagnostics.runDirectory ? `<div class="manifest-row"><span class="label">Run Dir</span><span class="value">${esc(diagnostics.runDirectory)}</span></div>` : ''}
            <div class="manifest-row"><span class="label">Output</span><span class="value">${formatBytes((diagnostics.stdoutBytes || 0) + (diagnostics.stderrBytes || 0))}${diagnostics.outputBytesDiscarded ? ` · ${formatBytes(diagnostics.outputBytesDiscarded)} discarded` : ''}</span></div>
            <div class="manifest-row"><span class="label">Structured Result</span><span class="value">${diagnostics.structuredResultFound ? 'found' : 'missing'}</span></div>
        </div>
        <div class="detail-card" style="margin-bottom:16px">
            <h3>Resource Cleanup</h3>
            <div class="manifest-row"><span class="label">Process Group</span><span class="value">${cleanup.processGroupTerminated ? (cleanup.processGroupForceKilled ? 'force killed' : 'terminated') : 'clear'}</span></div>
            <div class="manifest-row"><span class="label">Detached Killed</span><span class="value">${cleanup.detachedProcessesKilled || 0}</span></div>
            <div class="manifest-row"><span class="label">Run Dir Removed</span><span class="value">${cleanup.runDirectoryRemoved ? 'yes' : 'no'}</span></div>
            ${cleanup.orphanScanError ? `<div class="delivery-error">${esc(cleanup.orphanScanError)}</div>` : ''}
            ${cleanup.runDirectoryCleanupError ? `<div class="delivery-error">${esc(cleanup.runDirectoryCleanupError)}</div>` : ''}
        </div>` : ''}
        ${run.outcome?.parsedResult?.summary ? `
        <div class="detail-card" style="margin-bottom:16px">
            <h3>Summary</h3>
            <p>${esc(run.outcome.parsedResult.summary)}</p>
        </div>` : ''}
        ${deliveryHTML}
        <h3 style="margin:16px 0 8px;font-size:14px;color:var(--text-secondary)">STDOUT</h3>
        <div class="log-block">${esc(run.outcome?.stdout || '(empty)')}</div>
        ${run.outcome?.stderr ? `
        <h3 style="margin:16px 0 8px;font-size:14px;color:var(--text-secondary)">STDERR</h3>
        <div class="log-block">${esc(run.outcome.stderr)}</div>` : ''}
    `;
}

// ===== Delivery =====

function renderDelivery() {
    return `
        <div class="page-header" style="display:flex;justify-content:space-between;align-items:start">
            <div>
                <h1>Delivery Profiles</h1>
                <p>Configure where task results are sent</p>
            </div>
            <button class="btn btn-primary" onclick="showAddProfileModal()">+ Add Profile</button>
        </div>
        <div id="delivery-list-content">${renderDeliveryList()}</div>
        <div id="profile-modal"></div>
    `;
}

function renderDeliveryList() {
    return `
        ${appState.deliveries.length === 0 ?
            '<div class="empty-state"><div class="icon">📬</div><h3>No delivery profiles</h3><p>Add a Telegram profile to receive task results.</p></div>' :
            `<div class="task-list">${appState.deliveries.map(p => `
                <div class="task-row" style="cursor:default">
                    <div class="task-info">
                        <div class="task-name">${esc(p.name)}</div>
                        <div class="task-meta">
                            ID: <span style="font-family:var(--font-mono)">${esc(p.id)}</span>
                            · ${esc(p.driverType)}
                            ${p.configFields?.botToken ? '· Bot token set' : '· <span class="badge badge-warning">missing bot token</span>'}
                            ${p.configFields?.chatID ? '· Chat ID set' : '· <span class="badge badge-warning">missing chat ID</span>'}
                            ${p.enabled ? '' : '· Disabled'}
                            ${p.inboundCommandsEnabled ? '· Commands enabled' : ''}
                            ${(p.usedByTasks || []).length ? `· Used by ${p.usedByTasks.length} task${p.usedByTasks.length === 1 ? '' : 's'}` : ''}
                        </div>
                        ${renderDeliveryUsage(p)}
                    </div>
                    <button class="btn btn-sm" data-profile-action="edit" data-profile-id="${esc(p.id)}">Edit</button>
                    <button class="btn btn-sm" data-profile-action="toggle-commands" data-profile-id="${esc(p.id)}" data-profile-enabled="${!p.inboundCommandsEnabled}">${p.inboundCommandsEnabled ? 'Disable Commands' : 'Enable Commands'}</button>
                    <button class="btn btn-sm" data-profile-action="test" data-profile-id="${esc(p.id)}">Test</button>
                    <button class="btn btn-sm btn-danger" data-profile-action="delete" data-profile-id="${esc(p.id)}">Delete</button>
                </div>
            `).join('')}</div>`
        }
    `;
}

function showEditProfileModal(id) {
    const profile = appState.deliveries.find(p => p.id === id);
    if (!profile) {
        toast('Profile not found', 'error');
        return;
    }
    const existing = document.getElementById('edit-profile-modal');
    if (existing) existing.remove();
    const div = document.createElement('div');
    div.id = 'edit-profile-modal';
    div.dataset.profileId = id;
    div.innerHTML = `
        <div class="modal-overlay" onclick="if(event.target===this)this.remove()">
            <div class="modal">
                <h2>Edit Telegram Profile</h2>
                <div class="form-group">
                    <label class="form-label">Profile Name</label>
                    <input class="form-input" id="edit-profile-name" value="${esc(profile.name)}" />
                </div>
                <div class="form-group">
                    <label class="form-label">Profile ID</label>
                    <input class="form-input" value="${esc(profile.id)}" disabled />
                </div>
                <div class="form-group">
                    <label class="form-label">New Bot Token</label>
                    <input class="form-input" id="edit-profile-token" type="password" placeholder="${profile.configFields?.botToken ? 'Leave blank to keep existing token' : '123456:ABC-DEF...'}" />
                </div>
                <div class="form-group">
                    <label class="form-label">New Chat ID</label>
                    <input class="form-input" id="edit-profile-chat" placeholder="${profile.configFields?.chatID ? 'Leave blank to keep existing chat ID' : '-100123456789'}" />
                    ${latestCommandChatID() ? '<button class="btn btn-sm inline-form-action" onclick="useLatestCommandChat(\'edit-profile-chat\')">Use latest command chat</button>' : ''}
                </div>
                <div class="form-group">
                    <label class="form-label">Authorized Chat IDs</label>
                    <textarea class="form-input" id="edit-profile-authorized" rows="3" placeholder="One chat ID per line">${esc((profile.authorizedChatIDs || []).join('\\n'))}</textarea>
                </div>
                <label class="checkbox-row">
                    <input type="checkbox" id="edit-profile-enabled" ${profile.enabled ? 'checked' : ''} />
                    <span>Profile enabled</span>
                </label>
                <label class="checkbox-row" style="margin-top:10px">
                    <input type="checkbox" id="edit-profile-commands" ${profile.inboundCommandsEnabled ? 'checked' : ''} />
                    <span>Enable Telegram commands</span>
                </label>
                <div class="modal-actions">
                    <button class="btn" onclick="document.getElementById('edit-profile-modal').remove()">Cancel</button>
                    <button class="btn btn-primary" onclick="updateProfileFromModal()">Save</button>
                </div>
            </div>
        </div>
    `;
    document.body.appendChild(div);
    document.getElementById('edit-profile-name').focus();
}

function showAddProfileModal() {
    document.getElementById('profile-modal').innerHTML = `
        <div class="modal-overlay" onclick="if(event.target===this)this.remove()">
            <div class="modal">
                <h2>Add Telegram Profile</h2>
                <div class="form-group">
                    <label class="form-label">Profile Name</label>
                    <input class="form-input" id="new-profile-name" value="My Telegram" oninput="syncNewProfileID()" />
                </div>
                <div class="form-group">
                    <label class="form-label">Profile ID</label>
                    <input class="form-input" id="new-profile-id" value="my-telegram" placeholder="my-telegram" oninput="this.dataset.touched='true'" />
                </div>
                <div class="form-group">
                    <label class="form-label">Bot Token</label>
                    <input class="form-input" id="new-profile-token" type="password" placeholder="123456:ABC-DEF..." />
                </div>
                <div class="form-group">
                    <label class="form-label">Chat ID</label>
                    <input class="form-input" id="new-profile-chat" placeholder="-100123456789" />
                    ${latestCommandChatID() ? '<button class="btn btn-sm inline-form-action" onclick="useLatestCommandChat(\'new-profile-chat\')">Use latest command chat</button>' : ''}
                </div>
                <label class="checkbox-row">
                    <input type="checkbox" id="new-profile-commands" />
                    <span>Enable Telegram commands for this chat</span>
                </label>
                <div class="modal-actions">
                    <button class="btn" onclick="document.getElementById('profile-modal').innerHTML=''">Cancel</button>
                    <button class="btn btn-primary" onclick="createProfile()">Create</button>
                </div>
            </div>
        </div>
    `;
}

function syncNewProfileID() {
    const nameInput = document.getElementById('new-profile-name');
    const idInput = document.getElementById('new-profile-id');
    if (!nameInput || !idInput || idInput.dataset.touched === 'true') return;
    idInput.value = slugifyProfileID(nameInput.value);
}

function slugifyProfileID(value) {
    return String(value || '')
        .toLowerCase()
        .replace(/[^a-z0-9_-]+/g, '-')
        .replace(/-+/g, '-')
        .replace(/^-|-$/g, '');
}

function deliveryIDPath(id) {
    return encodeURIComponent(id);
}

async function createProfile() {
    const name = document.getElementById('new-profile-name').value.trim();
    const botToken = document.getElementById('new-profile-token').value.trim();
    const chatID = document.getElementById('new-profile-chat').value.trim();
    const idInput = document.getElementById('new-profile-id');
    const profileID = slugifyProfileID(idInput.value.trim() || name);
    if (!name || !botToken || !chatID) {
        toast('Name, bot token, and chat ID are required', 'error');
        return;
    }
    if (!profileID) {
        toast('Profile ID must include letters or numbers', 'error');
        return;
    }
    const profile = {
        name,
        driverType: 'telegram',
        enabled: true,
        inboundCommandsEnabled: document.getElementById('new-profile-commands').checked,
        config: {
            bot_token: botToken,
            chat_id: chatID
        }
    };
    if (idInput.dataset.touched === 'true') {
        profile.id = profileID;
    }
    const result = await api('POST', '/api/deliveries', profile);
    if (result?.error) {
        toast(result.message || 'Profile could not be created', 'error');
        return;
    }
    document.getElementById('profile-modal').innerHTML = '';
    toast('Profile created', 'success');
    refreshAll();
}

function updateProfileFromModal() {
    const modal = document.getElementById('edit-profile-modal');
    if (!modal) return;
    updateProfile(modal.dataset.profileId || '');
}

async function updateProfile(id) {
    const name = document.getElementById('edit-profile-name').value.trim();
    const botToken = document.getElementById('edit-profile-token').value.trim();
    const chatID = document.getElementById('edit-profile-chat').value.trim();
    const authorizedText = document.getElementById('edit-profile-authorized').value;
    if (!name) {
        toast('Profile name is required', 'error');
        return;
    }
    const config = {};
    if (botToken) config.bot_token = botToken;
    if (chatID) config.chat_id = chatID;
    const authorizedChatIDs = authorizedText
        .split(/[\n,]+/)
        .map(v => v.trim())
        .filter(Boolean);
    const profile = {
        name,
        driverType: 'telegram',
        enabled: document.getElementById('edit-profile-enabled').checked,
        inboundCommandsEnabled: document.getElementById('edit-profile-commands').checked,
        authorizedChatIDs,
        config
    };
    const result = await api('PUT', `/api/deliveries/${deliveryIDPath(id)}`, profile);
    if (result?.error) {
        toast(result.message || 'Profile could not be updated', 'error');
        return;
    }
    document.getElementById('edit-profile-modal').remove();
    toast('Profile updated', 'success');
    refreshAll();
}

async function deleteProfile(id) {
    if (!confirm('Delete this delivery profile?')) return;
    const result = await api('DELETE', `/api/deliveries/${deliveryIDPath(id)}`);
    if (result?.error) {
        toast(result.message || 'Profile could not be deleted', 'error');
        return;
    }
    toast('Profile deleted', 'info');
    refreshAll();
}

async function testProfile(id) {
    const result = await api('POST', `/api/deliveries/${deliveryIDPath(id)}/test`, { message: 'CronPlus delivery test' });
    if (result?.error) {
        toast(explainDeliveryError(result.message || 'Delivery test failed'), 'error');
        return;
    }
    toast('Test message sent', 'success');
}

async function toggleProfileCommands(id, enabled) {
    const action = enabled ? 'enable' : 'disable';
    const result = await api('POST', `/api/deliveries/${deliveryIDPath(id)}/commands/${action}`);
    if (result?.error) {
        toast(result.message || 'Command setting could not be changed', 'error');
        return;
    }
    toast(enabled ? 'Commands enabled' : 'Commands disabled', 'success');
    refreshAll();
}

function renderDeliveryUsage(profile) {
    const usedBy = profile.usedByTasks || [];
    if (!usedBy.length) return '';
    return `<div class="usage-list">
        ${usedBy.slice(0, 4).map(task => `<a href="#/tasks/${esc(task.id)}" onclick="event.stopPropagation()">${esc(task.name)}</a>`).join('')}
        ${usedBy.length > 4 ? `<span>${usedBy.length - 4} more</span>` : ''}
    </div>`;
}

function latestCommandChatID() {
    const command = appState.commands.find(c => c.chatID);
    return command?.chatID || '';
}

function useLatestCommandChat(inputID) {
    const input = document.getElementById(inputID);
    const chatID = latestCommandChatID();
    if (!input || !chatID) return;
    input.value = chatID;
    toast('Chat ID filled', 'success');
}

function explainDeliveryError(message) {
    const text = String(message || '');
    const lower = text.toLowerCase();
    if (lower.includes('missing bot_token') || lower.includes('missing') && lower.includes('chat_id')) {
        return 'Telegram profile is missing a bot token or chat ID.';
    }
    if (lower.includes('401') || lower.includes('unauthorized')) {
        return 'Telegram rejected the bot token.';
    }
    if (lower.includes('400') && lower.includes('chat')) {
        return 'Telegram could not find that chat ID.';
    }
    if (lower.includes('403') || lower.includes('blocked')) {
        return 'Telegram cannot send to this chat. The bot may be blocked or not in the chat.';
    }
    if (lower.includes('request failed') || lower.includes('timeout')) {
        return 'Telegram request failed. Check network access and try again.';
    }
    return text;
}

// ===== Commands =====

function renderCommands() {
    return `
        <div class="page-header" style="display:flex;justify-content:space-between;align-items:start">
            <div>
                <h1>Command Log</h1>
                <p>Inbound commands received from channels</p>
            </div>
            ${appState.commands.length > 0 ? '<button class="btn btn-danger btn-sm" onclick="clearCommands()">Clear Log</button>' : ''}
        </div>
        <div id="commands-content">${renderCommandsContent()}</div>
    `;
}

function renderCommandsContent() {
    return `
        ${appState.commands.length === 0 ?
            '<div class="empty-state"><div class="icon">💬</div><h3>No commands received</h3></div>' :
            `<div class="table-wrapper"><table>
                <thead><tr><th>Command</th><th>Chat</th><th>Reply / Error</th><th>Time</th></tr></thead>
                <tbody>${appState.commands.map(c => `
                    <tr>
                        <td style="font-family:var(--font-mono)">${esc(c.commandText)}</td>
                        <td>${esc(c.chatID)}</td>
                        <td>${renderCommandReplyCell(c)}</td>
                        <td>${formatTime(c.receivedAt)}</td>
                    </tr>
                `).join('')}</tbody>
            </table></div>`
        }
    `;
}

function renderCommandReplyCell(command) {
    if (command.error) {
        return `<span class="delivery-error">${esc(truncateText(command.error, 100))}</span>`;
    }
    return esc(truncateText(command.replyText || '', 100));
}

function truncateText(value, maxLength) {
    const text = String(value || '');
    if (text.length <= maxLength) return text;
    return text.slice(0, Math.max(0, maxLength - 3)) + '...';
}

async function clearCommands() {
    const result = await api('DELETE', '/api/commands');
    if (result?.error) {
        toast(result.message || 'Command log could not be cleared', 'error');
        return;
    }
    toast('Command log cleared', 'info');
    refreshAll();
}

// ===== Settings =====

function renderSettings() {
    const server = appState.status?.server || {};
    return `
        <div class="page-header">
            <h1>Settings</h1>
            <p>Daemon configuration</p>
        </div>
        <div class="detail-card" style="max-width:500px">
            <h3>Authentication</h3>
            <p style="margin-bottom:12px">Your auth token is stored at:</p>
            <div class="log-block" style="max-height:none">~/.config/cronplus/auth-token</div>
            <div style="display:flex;gap:12px;margin-top:12px">
                <button class="btn btn-sm" onclick="navigator.clipboard.writeText(authToken);toast('Token copied','success')">Copy Token</button>
                <button class="btn btn-sm" style="color:var(--danger);border-color:var(--danger)" onclick="logout()">Sign Out</button>
            </div>
        </div>
        <div class="detail-card" style="max-width:500px;margin-top:16px">
            <h3>Version</h3>
            <p id="settings-version">${appState.status?.version || 'N/A'}</p>
        </div>
        <div class="detail-card" style="max-width:720px;margin-top:16px">
            <h3>Daemon</h3>
            <div class="manifest-row"><span class="label">Web UI</span><span class="value">${server.addr ? `http://${esc(server.addr)}` : 'N/A'}</span></div>
            <div class="manifest-row"><span class="label">Config Dir</span><span class="value">${esc(server.configDir || 'N/A')}</span></div>
            <div class="manifest-row"><span class="label">State File</span><span class="value">${esc(server.statePath || 'N/A')}</span></div>
            <div class="manifest-row"><span class="label">Token File</span><span class="value">${esc(server.tokenPath || '~/.config/cronplus/auth-token')}</span></div>
            <div class="manifest-row"><span class="label">Max Runs</span><span class="value">${server.maxConcurrentRuns || 'N/A'}</span></div>
        </div>
        <div class="detail-card" style="max-width:500px;margin-top:16px">
            <h3>Offline Cache</h3>
            <button class="btn btn-sm" onclick="clearOfflineCache()">Clear Cache</button>
        </div>
    `;
}

// ===== Actions =====

async function runTask(id) {
    const result = await api('POST', `/api/tasks/${id}/run`);
    if (result?.error) {
        toast(result.message || 'Run could not be started', 'error');
        return;
    }
    toast('Run started', 'success');
    refreshAll();
}

async function toggleTask(id, enabled) {
    const result = await api('POST', `/api/tasks/${id}/${enabled ? 'enable' : 'disable'}`);
    if (result?.error) {
        toast(result.message || 'Task could not be updated', 'error');
        return;
    }
    delete appState.taskDetails[id];
    toast(enabled ? 'Task enabled' : 'Task disabled', 'success');
    refreshAll();
}

async function reloadTask(id) {
    const result = await api('POST', `/api/tasks/${id}/reload`);
    if (result?.error) {
        toast(result.message || 'Manifest reload failed', 'error');
        return;
    }
    delete appState.taskDetails[id];
    toast('Manifest reloaded', 'success');
    refreshAll();
}

async function checkImportedTask(id) {
    const target = document.getElementById(`task-check-result-${id}`);
    if (target) {
        target.innerHTML = '<div class="check-panel"><h3>Package Check</h3><p>Checking package...</p></div>';
    }
    const result = await api('POST', `/api/tasks/${id}/check`);
    if (result?.error) {
        if (target) target.innerHTML = `<div class="delivery-error">${esc(result.message || 'Package check failed')}</div>`;
        toast(result.message || 'Package check failed', 'error');
        return;
    }
    appState.taskChecks[id] = result;
    if (target) {
        target.innerHTML = renderTaskPackageCheck(result);
    }
    toast(checkToastMessage(result), result.status === 'failure' ? 'error' : 'success');
}

async function previewDelivery(id) {
    const result = await api('GET', `/api/tasks/${id}/delivery-preview`);
    if (result?.error) {
        toast(result.message || 'No delivery preview available', 'error');
        return;
    }
    deliveryPreviewText = result.message || '';
    showDeliveryPreview(deliveryPreviewText);
}

function showDeliveryPreview(message) {
    const existing = document.getElementById('preview-modal');
    if (existing) existing.remove();
    const div = document.createElement('div');
    div.id = 'preview-modal';
    div.innerHTML = `
        <div class="modal-overlay" onclick="if(event.target===this)this.remove()">
            <div class="modal modal-wide">
                <h2>Delivery Preview</h2>
                <div class="preview-block">${esc(message || '(empty)')}</div>
                <div class="modal-actions">
                    <button class="btn" onclick="copyDeliveryPreview()">Copy</button>
                    <button class="btn btn-primary" onclick="document.getElementById('preview-modal').remove()">Close</button>
                </div>
            </div>
        </div>
    `;
    document.body.appendChild(div);
}

async function copyDeliveryPreview() {
    try {
        await navigator.clipboard.writeText(deliveryPreviewText);
        toast('Preview copied', 'success');
    } catch {
        toast('Copy failed', 'error');
    }
}

async function removeTaskImport(id) {
    if (!confirm('Remove this import from CronPlus? The task package files are not deleted. Run history for this import is removed.')) return;
    const result = await api('DELETE', `/api/tasks/${id}`);
    if (result?.error) {
        toast(result.message || 'Import could not be removed', 'error');
        return;
    }
    delete appState.taskDetails[id];
    toast('Import removed', 'info');
    window.location.hash = '#/tasks';
    refreshAll();
}

function promptImportTask() {
    // Show modal instead of prompt()
    const existing = document.getElementById('import-modal');
    if (existing) existing.remove();
    const div = document.createElement('div');
    div.id = 'import-modal';
    div.innerHTML = `
        <div class="modal-overlay" onclick="if(event.target===this)this.remove()">
            <div class="modal">
                <h2>Import Task Package</h2>
                <div class="form-group">
                    <label class="form-label">Package Directory</label>
                    <input class="form-input" id="import-path-input" placeholder="/path/to/my-task" style="font-family:var(--font-mono)" autofocus>
                    <p style="font-size:12px;color:var(--text-muted);margin-top:8px">Full path to a directory containing a .cronplus.yaml manifest</p>
                </div>
                <div id="import-error" style="color:var(--danger);font-size:13px;display:none;margin-bottom:12px"></div>
                <div id="import-check-result"></div>
                <div class="modal-actions">
                    <button class="btn" onclick="document.getElementById('import-modal').remove()">Cancel</button>
                    <button class="btn" onclick="checkImportPackage()">Check Package</button>
                    <button class="btn btn-primary" onclick="doImportTask()">Import</button>
                </div>
            </div>
        </div>
    `;
    document.body.appendChild(div);
    document.getElementById('import-path-input').focus();
    document.getElementById('import-path-input').addEventListener('keydown', e => {
        if (e.key === 'Enter') doImportTask();
    });
}

async function checkImportPackage() {
    const input = document.getElementById('import-path-input');
    const resultEl = document.getElementById('import-check-result');
    const path = input.value.trim();
    if (!path) {
        input.style.borderColor = 'var(--danger)';
        return;
    }
    resultEl.innerHTML = '<div class="check-panel"><h3>Package Check</h3><p>Checking package...</p></div>';
    const result = await api('POST', '/api/tasks/check', { path });
    if (result?.error) {
        resultEl.innerHTML = `<div class="delivery-error">${esc(result.message || 'Package check failed')}</div>`;
        return;
    }
    resultEl.innerHTML = renderTaskPackageCheck(result);
}

async function doImportTask() {
    const input = document.getElementById('import-path-input');
    const errEl = document.getElementById('import-error');
    const path = input.value.trim();
    if (!path) { input.style.borderColor = 'var(--danger)'; return; }

    const result = await api('POST', '/api/tasks/import', { path });
    if (result && result.id) {
        document.getElementById('import-modal').remove();
        toast(`Imported "${result.name}"`, 'success');
        window.location.hash = `#/tasks/${result.id}`;
        refreshAll();
    } else if (result && result.message) {
        errEl.textContent = result.message;
        errEl.style.display = 'block';
    } else if (result?.error) {
        errEl.textContent = result.message || 'Import failed';
        errEl.style.display = 'block';
    }
}

async function importTask(path) {
    const result = await api('POST', '/api/tasks/import', { path });
    if (result && result.id) {
        toast(`Imported "${result.name}"`, 'success');
        window.location.hash = `#/tasks/${result.id}`;
    } else if (result?.error) {
        toast(result.message || 'Import failed', 'error');
    }
    refreshAll();
}

// ===== Helpers =====

function setConnectionState(connected) {
    appState.connected = connected;
    const dot = document.querySelector('.status-dot');
    const text = document.querySelector('.status-text');
    if (!dot || !text) return;
    dot.classList.toggle('disconnected', !connected);
    text.textContent = connected ? 'Connected' : 'Disconnected';
}

function loadOfflineCache() {
    try {
        OFFLINE_CACHE_KEYS_TO_CLEAR.forEach(key => localStorage.removeItem(key));
        const cached = JSON.parse(localStorage.getItem(OFFLINE_CACHE_KEY) || '{}');
        if (cacheExpired(cached.savedAt)) {
            localStorage.removeItem(OFFLINE_CACHE_KEY);
            return;
        }
        if (cached.status) appState.status = cached.status;
        if (Array.isArray(cached.tasks)) appState.tasks = cached.tasks;
        if (cached.taskDetails && typeof cached.taskDetails === 'object') appState.taskDetails = sanitizeTaskMapForCache(cached.taskDetails);
        if (cached.runHistories && typeof cached.runHistories === 'object') appState.runHistories = sanitizeRunHistoriesForCache(cached.runHistories);
        if (Array.isArray(cached.deliveries)) appState.deliveries = cached.deliveries;
        appStateSignatures.status = stateSignature(appState.status);
        appStateSignatures.tasks = stateSignature(appState.tasks);
        appStateSignatures.deliveries = stateSignature(appState.deliveries);
    } catch {
        localStorage.removeItem(OFFLINE_CACHE_KEY);
    }
}

function saveOfflineCache() {
    try {
        localStorage.setItem(OFFLINE_CACHE_KEY, JSON.stringify({
            status: appState.status,
            tasks: appState.tasks,
            taskDetails: sanitizeTaskMapForCache(appState.taskDetails),
            runHistories: sanitizeRunHistoriesForCache(appState.runHistories),
            deliveries: appState.deliveries,
            savedAt: new Date().toISOString()
        }));
    } catch {
        // Ignore quota or private-mode storage failures; live state still works.
    }
}

function clearOfflineCache() {
    localStorage.removeItem(OFFLINE_CACHE_KEY);
    OFFLINE_CACHE_KEYS_TO_CLEAR.forEach(key => localStorage.removeItem(key));
    appState.taskDetails = {};
    appState.runHistories = {};
    appState.runDetails = {};
    toast('Offline cache cleared', 'info');
    if (currentPage.startsWith('/tasks')) renderCurrentPage();
}

function cacheExpired(savedAt) {
    if (!savedAt) return false;
    const savedTime = Date.parse(savedAt);
    if (!Number.isFinite(savedTime)) return true;
    return Date.now() - savedTime > OFFLINE_CACHE_MAX_AGE_MS;
}

function sanitizeTaskMapForCache(taskDetails) {
    return Object.fromEntries(Object.entries(taskDetails || {}).map(([id, task]) => [id, sanitizeTaskForCache(task)]));
}

function sanitizeTaskForCache(task) {
    const copy = cloneJSON(task);
    if (!copy || typeof copy !== 'object') return copy;
    if (copy.manifest?.runtime?.env) {
        copy.manifest.runtime.env = {};
    }
    if (Array.isArray(copy.manifest?.delivery?.inlineProfiles)) {
        copy.manifest.delivery.inlineProfiles = copy.manifest.delivery.inlineProfiles.map(profile => ({
            ...profile,
            config: {}
        }));
    }
    return copy;
}

function sanitizeRunHistoriesForCache(runHistories) {
    return Object.fromEntries(Object.entries(runHistories || {}).map(([taskID, runs]) => [
        taskID,
        Array.isArray(runs) ? runs.map(sanitizeRunForCache) : []
    ]));
}

function sanitizeRunForCache(run) {
    if (!run || typeof run !== 'object') return run;
    const outcome = run.outcome || {};
    const parsed = outcome.parsedResult ? {
        status: outcome.parsedResult.status || '',
        summary: outcome.parsedResult.summary || ''
    } : undefined;
    const sanitized = {
        id: run.id,
        taskID: run.taskID,
        trigger: run.trigger,
        startedAt: run.startedAt,
        finishedAt: run.finishedAt,
        deliveryResults: Array.isArray(run.deliveryResults) ? cloneJSON(run.deliveryResults) : [],
        outcome: {
            exitCode: outcome.exitCode,
            timedOut: !!outcome.timedOut,
            durationMs: outcome.durationMs || 0
        }
    };
    if (parsed) {
        sanitized.outcome.parsedResult = parsed;
    }
    return sanitized;
}

function cloneJSON(value) {
    if (value === undefined || value === null) return value;
    return JSON.parse(JSON.stringify(value));
}

function esc(s) {
    if (!s) return '';
    const div = document.createElement('div');
    div.textContent = String(s);
    return div.innerHTML;
}

function attr(s) {
    return String(s || '')
        .replace(/&/g, '&amp;')
        .replace(/"/g, '&quot;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
}

function renderInlineState(title, message, tone = 'neutral', actionLabel = '', actionAttrs = '') {
    return `
        <div class="inline-state inline-state-${esc(tone)}">
            <div>
                <h3>${esc(title)}</h3>
                ${message ? `<p>${esc(message)}</p>` : ''}
            </div>
            ${actionLabel && actionAttrs ? `<button class="btn btn-sm" ${actionAttrs}>${esc(actionLabel)}</button>` : ''}
        </div>
    `;
}

function renderInlineNotice(message, tone = 'warning') {
    if (!message) return '';
    return `<div class="inline-notice inline-notice-${esc(tone)}">${esc(message)}</div>`;
}

function renderNextRuns(times) {
    if (!Array.isArray(times) || times.length === 0) return '';
    return `<div class="next-run-list">
        ${times.map((time, index) => `
            <div class="next-run-row">
                <span>${index === 0 ? 'Next' : `#${index + 1}`}</span>
                <strong>${formatTime(time)}</strong>
            </div>
        `).join('')}
    </div>`;
}

function renderTaskPackageCheck(result) {
    if (!result) return '';
    const issues = result.issues || [];
    const run = result.run;
    return `
        <div class="check-panel check-${esc(result.status || 'warning')}">
            <div class="check-header">
                <div>
                    <h3>Package Check</h3>
                    <p>${esc(result.summary || 'Check complete.')}</p>
                </div>
                <span class="badge badge-${statusBadgeClass(result.status)}">${esc(result.status || 'unknown')}</span>
            </div>
            <div class="check-grid">
                <div class="check-step">
                    <span>Manifest</span>
                    <strong>${issues.some(i => i.severity === 'error') ? 'failed' : 'valid'}</strong>
                </div>
                <div class="check-step">
                    <span>Environment</span>
                    <strong>${esc(result.environment?.status || 'pending')}</strong>
                </div>
                <div class="check-step">
                    <span>Run</span>
                    <strong>${esc(run?.status || 'not run')}</strong>
                </div>
            </div>
            ${result.name ? `<div class="manifest-row"><span class="label">Task</span><span class="value">${esc(result.name)}</span></div>` : ''}
            ${result.manifestPath ? `<div class="manifest-row"><span class="label">Manifest</span><span class="value">${esc(result.manifestPath)}</span></div>` : ''}
            ${renderNextRuns(result.nextRuns || [])}
            ${issues.length ? `<div class="check-issues">
                ${issues.map(issue => `
                    <div class="check-issue ${esc(issue.severity)}">
                        <span>${esc(issue.severity)}</span>
                        <strong>${esc(issue.path)}</strong>
                        <p>${esc(issue.message)}</p>
                    </div>
                `).join('')}
            </div>` : ''}
            ${result.environment?.status === 'failure' ? `<div class="delivery-error">${esc(result.environment.message)}</div>` : ''}
            ${run ? renderRunDiagnosis({ id: `check-${Date.now()}`, diagnosis: run.diagnostics, outcome: { exitCode: run.exitCode, timedOut: run.timedOut, durationMs: run.durationMs } }, { compact: true }) : ''}
            ${run?.stdoutTail ? `<h3 class="check-log-title">STDOUT</h3><div class="log-block check-log">${esc(run.stdoutTail)}</div>` : ''}
            ${run?.stderrTail ? `<h3 class="check-log-title">STDERR</h3><div class="log-block check-log">${esc(run.stderrTail)}</div>` : ''}
        </div>
    `;
}

function renderRunDiagnosis(run, options = {}) {
    const diagnosis = run.diagnosis || {};
    if (!diagnosis.summary && !diagnosis.status) return '';
    const causes = diagnosis.causes || [];
    const actions = diagnosis.actions || [];
    return `
        <div class="detail-card diagnosis-card diagnosis-${esc(diagnosis.status || 'warning')}">
            <div class="diagnosis-header">
                <div>
                    <h3>${options.compact ? 'Run Result' : 'What Happened'}</h3>
                    <p>${esc(diagnosis.summary || 'Run finished.')}</p>
                </div>
                <span class="badge badge-${statusBadgeClass(diagnosis.status)}">${esc(diagnosis.status || 'unknown')}</span>
            </div>
            ${causes.length ? `<div class="diagnosis-list">
                <span>Causes</span>
                ${causes.map(cause => `<p>${esc(cause)}</p>`).join('')}
            </div>` : ''}
            ${actions.length ? `<div class="diagnosis-list">
                <span>Next Actions</span>
                ${actions.map(action => `<p>${esc(action)}</p>`).join('')}
            </div>` : ''}
            ${!options.compact ? `<button class="btn btn-sm" onclick="copyRunDiagnostics('${esc(run.id)}')">Copy Diagnostics</button>` : ''}
        </div>
    `;
}

function copyRunDiagnostics(runID) {
    const run = appState.runDetails[runID];
    if (!run) {
        toast('Diagnostics not loaded', 'error');
        return;
    }
    const text = [
        `run_id=${run.id}`,
        `task_id=${run.taskID}`,
        `trigger=${run.trigger}`,
        `status=${run.diagnosis?.status || runStatusFor(run)}`,
        `summary=${run.diagnosis?.summary || ''}`,
        `exit_code=${run.outcome?.exitCode}`,
        `timed_out=${!!run.outcome?.timedOut}`,
        `duration_ms=${run.outcome?.durationMs || 0}`,
        `python=${run.outcome?.diagnostics?.pythonExecutable || ''}`,
        `script=${run.outcome?.diagnostics?.scriptPath || ''}`,
        `cwd=${run.outcome?.diagnostics?.workingDirectory || ''}`,
        '',
        'STDERR',
        run.outcome?.stderr || ''
    ].join('\n');
    navigator.clipboard.writeText(text)
        .then(() => toast('Diagnostics copied', 'success'))
        .catch(() => toast('Copy failed', 'error'));
}

function statusBadgeClass(status) {
    const normalized = normalizeRunStatus(status);
    if (normalized === 'success') return 'success';
    if (normalized === 'warning' || normalized === 'skipped') return 'warning';
    if (normalized === 'unknown') return 'muted';
    return 'danger';
}

function checkToastMessage(result) {
    if (result?.status === 'success') return 'Package check passed';
    if (result?.status === 'warning') return 'Package check passed with warnings';
    return result?.summary || 'Package check failed';
}

function runStatusFor(run) {
    const parsedStatus = run?.outcome?.parsedResult?.status;
    if (parsedStatus) return normalizeRunStatus(parsedStatus);
    const exitCode = run?.outcome?.exitCode;
    if (exitCode === 0) return 'success';
    if (exitCode !== undefined && exitCode !== null) return 'failure';
    return normalizeRunStatus(run?.status || 'unknown');
}

function normalizeRunStatus(status) {
    return String(status || '').toLowerCase() === 'failed' ? 'failure' : String(status || '').toLowerCase();
}

function runStatusBadge(status) {
    const normalized = normalizeRunStatus(status);
    if (normalized === 'success') return 'success';
    if (normalized === 'warning' || normalized === 'skipped') return 'warning';
    return 'danger';
}

function runHistoryStatusLabel(status) {
    const normalized = normalizeRunStatus(status);
    if (normalized === 'success') return 'run succeeded';
    if (normalized === 'warning') return 'run warned';
    if (normalized === 'skipped') return 'run skipped';
    if (normalized === 'unknown') return 'run status unknown';
    return 'run failed';
}

function deliveryStatusBadge(status) {
    if (status === 'success') return 'success';
    if (status === 'skipped') return 'muted';
    return 'danger';
}

function deliveryHistorySummary(results) {
    if (!Array.isArray(results) || results.length === 0) {
        return { status: 'none', label: 'delivery not sent' };
    }
    const counts = results.reduce((acc, result) => {
        const status = String(result.status || '').toLowerCase();
        if (status === 'success') acc.sent += 1;
        else if (status === 'skipped') acc.skipped += 1;
        else if (status === 'failed' || status === 'failure') acc.failed += 1;
        else acc.unknown += 1;
        return acc;
    }, { sent: 0, failed: 0, skipped: 0, unknown: 0 });
    const total = results.length;
    if (counts.failed > 0) {
        return counts.failed === total
            ? { status: 'failed', label: 'delivery failed' }
            : { status: 'failed', label: 'delivery partial' };
    }
    if (counts.sent > 0) return { status: 'success', label: 'delivery sent' };
    if (counts.skipped > 0) return { status: 'skipped', label: 'delivery skipped' };
    return { status: 'none', label: 'delivery unknown' };
}

function renderDeliveryHistorySummary(summary) {
    const badgeClass = summary.status === 'none' ? 'muted' : deliveryStatusBadge(summary.status);
    return `<span class="run-history-status-group delivery-history-status">
        <span class="badge badge-${badgeClass}">${esc(summary.label)}</span>
    </span>`;
}

function formatDurationMs(ms) {
    if (!ms) return '—';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
}

function formatBytes(bytes) {
    bytes = Number(bytes || 0);
    if (bytes < 1024) return `${bytes}B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

function formatTime(iso) {
    if (!iso) return '—';
    try {
        const d = new Date(iso);
        const now = new Date();
        const diffMs = now - d;

        if (diffMs > 0 && diffMs < 86400000) {
            if (diffMs < 60000) return 'just now';
            if (diffMs < 3600000) return `${Math.floor(diffMs/60000)}m ago`;
            return `${Math.floor(diffMs/3600000)}h ago`;
        }

        return d.toLocaleString(undefined, {
            month: 'short', day: 'numeric',
            hour: '2-digit', minute: '2-digit'
        });
    } catch { return iso; }
}

// ===== Toast Notifications =====

function toast(message, type = 'info') {
    const icons = { success: '✅', error: '❌', info: 'ℹ️' };
    const container = document.getElementById('toasts');
    const el = document.createElement('div');
    el.className = `toast toast-${type}`;
    el.innerHTML = `<span class="toast-icon">${icons[type] || icons.info}</span>${esc(message)}`;
    container.appendChild(el);
    setTimeout(() => {
        el.classList.add('toast-out');
        setTimeout(() => el.remove(), 300);
    }, 3500);
}

// ===== Logout =====

function logout() {
    localStorage.removeItem('cronplus_token');
    authToken = '';
    if (sseReconnectTimer) {
        clearTimeout(sseReconnectTimer);
        sseReconnectTimer = null;
    }
    if (sseConnection) {
        sseConnection.onopen = null;
        sseConnection.onerror = null;
        sseConnection.close();
        sseConnection = null;
    }
    showLogin();
}

// ===== Auto-refresh =====

setInterval(() => {
    if (document.getElementById('main-app').style.display !== 'none') {
        if (!hasActiveEditorState()) {
            refreshAll();
        }
    }
}, 30000);

function hasActiveEditorState() {
    if (document.querySelector('.modal-overlay')) return true;
    const active = document.activeElement;
    return !!active && active.matches('input, textarea, select, [contenteditable="true"]');
}

// ===== Boot =====
init();
