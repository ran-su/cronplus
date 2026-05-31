// CronPlus Web UI — Single Page Application

const API_BASE = '';
let authToken = localStorage.getItem('cronplus_token') || '';
let sseConnection = null;
let currentPage = 'dashboard';
let appState = { status: null, tasks: [], taskDetails: {}, taskDetailLoading: {}, taskDetailErrors: {}, deliveries: [], commands: [] };
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
        return {
            error: 'network_error',
            message: err?.message || 'Could not reach CronPlus'
        };
    }
}

async function refreshAll() {
    const [status, tasks, deliveries, commands] = await Promise.all([
        api('GET', '/api/status'),
        api('GET', '/api/tasks'),
        api('GET', '/api/deliveries'),
        api('GET', '/api/commands')
    ]);
    if (status && !status.error) appState.status = status;
    if (tasks && !tasks.error) {
        appState.tasks = tasks.tasks || [];
        appState.taskDetails = {};
        appState.taskDetailErrors = {};
    }
    if (deliveries && !deliveries.error) appState.deliveries = deliveries.profiles || [];
    if (commands && !commands.error) appState.commands = commands.commands || [];
    if (!hasActiveEditorState()) {
        renderCurrentPage();
    }
}

// ===== SSE =====

let sseRetryDelay = 1000;
const SSE_MAX_RETRY = 30000;

function connectSSE() {
    if (sseConnection) sseConnection.close();

    // SSE doesn't support custom headers, so we pass token as query param
    sseConnection = new EventSource(`${API_BASE}/api/events?token=${authToken}`);

    sseConnection.onopen = () => {
        sseRetryDelay = 1000;
        document.querySelector('.status-dot').classList.remove('disconnected');
        document.querySelector('.status-text').textContent = 'Connected';
    };

    sseConnection.onerror = () => {
        document.querySelector('.status-dot').classList.add('disconnected');
        document.querySelector('.status-text').textContent = 'Disconnected';
        sseConnection.close();
        setTimeout(() => {
            sseRetryDelay = Math.min(sseRetryDelay * 2, SSE_MAX_RETRY);
            connectSSE();
        }, sseRetryDelay);
    };

    sseConnection.addEventListener('run_started', () => refreshAll());
    sseConnection.addEventListener('run_completed', () => refreshAll());
    sseConnection.addEventListener('task_updated', () => refreshAll());
    sseConnection.addEventListener('status', (e) => {
        try {
            appState.status = JSON.parse(e.data);
            if (!hasActiveEditorState()) renderCurrentPage();
        } catch {}
    });
}

// ===== Router =====

function navigate(hash) {
    const path = hash.replace('#', '') || '/';
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

document.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && document.getElementById('login-screen').style.display !== 'none') {
        attemptLogin();
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

// ===== Dashboard =====

function renderDashboard() {
    const s = appState.status || {};
    const t = s.tasks || {};
    return `
        <div class="page-header">
            <h1>Dashboard</h1>
            <p>Overview of your automation tasks</p>
        </div>
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
            <a href="#/tasks" class="back-link">←</a>
            <h1>${esc(task.name)}</h1>
            <span class="badge badge-${task.enabled ? 'success' : 'muted'}">${task.enabled ? 'Enabled' : 'Disabled'}</span>
            ${task.manifestStatus?.changed ? '<span class="badge badge-warning">Manifest Changed</span>' : ''}
            <div style="margin-left:auto; display:flex; gap:12px; align-items:center;">
                <button class="btn btn-primary" onclick="runTask('${id}')">▶ Run Now</button>
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
                
                <h2 style="font-size:18px;margin-bottom:16px;">Run History</h2>
                <div id="run-history-${id}">Loading...</div>
            </div>

            <div class="task-sidebar">
                <div class="detail-card">
                    <h3>Schedule</h3>
                    <div style="font-family:var(--font-mono);font-size:18px;color:var(--accent);margin-bottom:8px;font-weight:600;letter-spacing:1px">${esc(task.scheduleSummary || 'N/A')}</div>
                    ${task.nextRun ? `<p style="color:var(--text-secondary);font-size:13px;display:flex;align-items:center;gap:6px;"><span class="task-status-dot running"></span> Next: ${formatTime(task.nextRun)}</p>` : ''}
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

async function loadTaskDetail(taskID) {
    if (appState.taskDetails[taskID] || appState.taskDetailLoading[taskID]) return;
    appState.taskDetailLoading[taskID] = true;
    const data = await api('GET', `/api/tasks/${taskID}`);
    delete appState.taskDetailLoading[taskID];
    if (!data || data.error) {
        delete appState.taskDetails[taskID];
        appState.taskDetailErrors[taskID] = data?.message || 'Task not found';
        if (currentPage === `/tasks/${taskID}`) renderCurrentPage();
        return;
    }
    delete appState.taskDetailErrors[taskID];
    appState.taskDetails[taskID] = data;
    if (currentPage === `/tasks/${taskID}`) {
        renderCurrentPage();
    }
}

async function loadRunHistory(taskID) {
    const data = await api('GET', `/api/tasks/${taskID}/runs`);
    const el = document.getElementById(`run-history-${taskID}`);
    if (!el || !data) return;
    if (data.error) {
        el.innerHTML = `<div class="delivery-error">${esc(data.message || 'Could not load run history')}</div>`;
        return;
    }

    const runs = data.runs || [];
    if (runs.length === 0) {
        el.innerHTML = '<div class="empty-state"><div class="icon">📭</div><h3>No runs yet</h3></div>';
        return;
    }

    el.innerHTML = `<div class="table-wrapper"><table>
        <thead><tr><th>Status</th><th>Trigger</th><th>Started</th><th>Duration</th><th></th></tr></thead>
        <tbody>${runs.map(r => {
            const status = normalizeRunStatus(r.outcome?.parsedResult?.status || (r.outcome?.exitCode === 0 ? 'success' : 'failure'));
            return `<tr>
                <td><span class="badge badge-${runStatusBadge(status)}">${esc(status)}</span></td>
                <td>${esc(r.trigger)}</td>
                <td>${formatTime(r.startedAt)}</td>
                <td>${r.outcome?.durationMs ? (r.outcome.durationMs / 1000).toFixed(1) + 's' : '—'}</td>
                <td><a href="#/tasks/${r.taskID}/runs/${r.id}" class="btn btn-sm">View</a></td>
            </tr>`;
        }).join('')}</tbody>
    </table></div>`;
}

// ===== Run Detail =====

function renderRunDetail(path) {
    const parts = path.split('/');
    const taskID = parts[2];
    const runID = parts[4];
    const task = appState.tasks.find(t => t.id === taskID);

    return `
        <div class="detail-header">
            <a href="#/tasks/${taskID}" class="back-link">← ${esc(task?.name || 'Task')}</a>
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
        el.innerHTML = `<div class="empty-state"><h3>${esc(run.message || 'Run not found')}</h3></div>`;
        return;
    }

    const status = normalizeRunStatus(run.outcome?.parsedResult?.status || (run.outcome?.exitCode === 0 ? 'success' : 'failure'));
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

    el.innerHTML = `
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
        ${appState.deliveries.length === 0 ?
            '<div class="empty-state"><div class="icon">📬</div><h3>No delivery profiles</h3><p>Add a Telegram profile to receive task results.</p></div>' :
            `<div class="task-list">${appState.deliveries.map(p => `
                <div class="task-row" style="cursor:default">
                    <div class="task-info">
                        <div class="task-name">${esc(p.name)}</div>
                        <div class="task-meta">${esc(p.driverType)} ${p.enabled ? '' : '· Disabled'} ${p.inboundCommandsEnabled ? '· Commands enabled' : ''}</div>
                    </div>
                    <button class="btn btn-sm" onclick="testProfile('${p.id}')">Test</button>
                    <button class="btn btn-sm btn-danger" onclick="deleteProfile('${p.id}')">Delete</button>
                </div>
            `).join('')}</div>`
        }
        <div id="profile-modal"></div>
    `;
}

function showAddProfileModal() {
    document.getElementById('profile-modal').innerHTML = `
        <div class="modal-overlay" onclick="if(event.target===this)this.remove()">
            <div class="modal">
                <h2>Add Telegram Profile</h2>
                <div class="form-group">
                    <label class="form-label">Profile Name</label>
                    <input class="form-input" id="new-profile-name" value="Telegram" />
                </div>
                <div class="form-group">
                    <label class="form-label">Bot Token</label>
                    <input class="form-input" id="new-profile-token" type="password" placeholder="123456:ABC-DEF..." />
                </div>
                <div class="form-group">
                    <label class="form-label">Chat ID</label>
                    <input class="form-input" id="new-profile-chat" placeholder="-100123456789" />
                </div>
                <div class="modal-actions">
                    <button class="btn" onclick="document.getElementById('profile-modal').innerHTML=''">Cancel</button>
                    <button class="btn btn-primary" onclick="createProfile()">Create</button>
                </div>
            </div>
        </div>
    `;
}

async function createProfile() {
    const profile = {
        name: document.getElementById('new-profile-name').value,
        driverType: 'telegram',
        enabled: true,
        config: {
            bot_token: document.getElementById('new-profile-token').value,
            chat_id: document.getElementById('new-profile-chat').value
        }
    };
    const result = await api('POST', '/api/deliveries', profile);
    if (result?.error) {
        toast(result.message || 'Profile could not be created', 'error');
        return;
    }
    document.getElementById('profile-modal').innerHTML = '';
    toast('Profile created', 'success');
    refreshAll();
}

async function deleteProfile(id) {
    if (!confirm('Delete this delivery profile?')) return;
    const result = await api('DELETE', `/api/deliveries/${id}`);
    if (result?.error) {
        toast(result.message || 'Profile could not be deleted', 'error');
        return;
    }
    toast('Profile deleted', 'info');
    refreshAll();
}

async function testProfile(id) {
    const result = await api('POST', `/api/deliveries/${id}/test`, { message: 'CronPlus delivery test' });
    if (result?.error) {
        toast(result.message || 'Delivery test failed', 'error');
        return;
    }
    toast('Test message sent', 'success');
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
        ${appState.commands.length === 0 ?
            '<div class="empty-state"><div class="icon">💬</div><h3>No commands received</h3></div>' :
            `<div class="table-wrapper"><table>
                <thead><tr><th>Command</th><th>Chat</th><th>Reply</th><th>Time</th></tr></thead>
                <tbody>${appState.commands.map(c => `
                    <tr>
                        <td style="font-family:var(--font-mono)">${esc(c.commandText)}</td>
                        <td>${esc(c.chatID)}</td>
                        <td>${esc((c.replyText || '').substring(0, 80))}${(c.replyText||'').length > 80 ? '...' : ''}</td>
                        <td>${formatTime(c.receivedAt)}</td>
                    </tr>
                `).join('')}</tbody>
            </table></div>`
        }
    `;
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
            <p>${appState.status?.version || 'N/A'}</p>
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
                <div class="modal-actions">
                    <button class="btn" onclick="document.getElementById('import-modal').remove()">Cancel</button>
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

function esc(s) {
    if (!s) return '';
    const div = document.createElement('div');
    div.textContent = String(s);
    return div.innerHTML;
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

function deliveryStatusBadge(status) {
    if (status === 'success') return 'success';
    if (status === 'skipped') return 'muted';
    return 'danger';
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
    if (sseConnection) sseConnection.close();
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
