// Transloader Frontend Application — Multi-Connection Direct Downloader
document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('transload-form');
  const urlInput = document.getElementById('url-input');
  const filenameInput = document.getElementById('filename-input');
  const connectionsSelect = document.getElementById('connections-select');
  const uaInput = document.getElementById('ua-input');
  const cookieInput = document.getElementById('cookie-input');
  const refererInput = document.getElementById('referer-input');
  const authInput = document.getElementById('auth-input');
  const customRawHeaders = document.getElementById('custom-raw-headers');
  const forceMediaCheckbox = document.getElementById('force-media-checkbox');
  const pasteBtn = document.getElementById('paste-btn');

  const activeTasksList = document.getElementById('active-tasks-list');
  const activeTasksCount = document.getElementById('active-tasks-count');
  const activeCountBadge = document.getElementById('active-count-badge');
  const tasksEmpty = document.getElementById('tasks-empty');

  const filesList = document.getElementById('files-list');
  const filesEmpty = document.getElementById('files-empty');
  const searchInput = document.getElementById('search-input');
  const clearAllBtn = document.getElementById('clear-all-btn');

  const ramVal = document.getElementById('ram-val');
  const diskVal = document.getElementById('disk-val');
  const diskBar = document.getElementById('disk-bar');

  let activeTasks = new Map();
  let cachedFiles = [];

  // 1. Initialize
  loadInitialData();
  connectSSE();

  // 2. Clipboard Paste Handler
  if (pasteBtn && navigator.clipboard) {
    pasteBtn.addEventListener('click', async () => {
      try {
        const text = await navigator.clipboard.readText();
        if (text && text.trim().startsWith('http')) {
          urlInput.value = text.trim();
          showToast('URL pasted from clipboard!', 'info');
        } else {
          showToast('Clipboard does not contain a valid URL', 'error');
        }
      } catch (err) {
        showToast('Clipboard permission denied', 'error');
      }
    });
  }

  // 3. Form Submit
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const rawURL = urlInput.value.trim();
    if (!rawURL) return;

    const conns = parseInt(connectionsSelect.value, 10) || 8;
    const headersMap = {};

    if (uaInput.value.trim()) headersMap['User-Agent'] = uaInput.value.trim();
    if (cookieInput.value.trim()) headersMap['Cookie'] = cookieInput.value.trim();
    if (refererInput.value.trim()) headersMap['Referer'] = refererInput.value.trim();
    if (authInput.value.trim()) headersMap['Authorization'] = authInput.value.trim();

    // Parse raw headers textarea (Key: Value)
    const rawLines = customRawHeaders.value.trim().split('\n');
    for (const line of rawLines) {
      const idx = line.indexOf(':');
      if (idx > 0) {
        const key = line.slice(0, idx).trim();
        const val = line.slice(idx + 1).trim();
        if (key && val) {
          headersMap[key] = val;
        }
      }
    }

    const payload = {
      url: rawURL,
      filename: filenameInput.value.trim(),
      connections: conns,
      headers: Object.keys(headersMap).length > 0 ? headersMap : undefined,
      force_ytdlp: forceMediaCheckbox.checked,
    };

    try {
      const res = await fetch('/api/download', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      const data = await res.json();
      if (!res.ok) {
        showToast(data.error || 'Failed to start download', 'error');
        return;
      }

      showToast(`Multi-connection download started (${conns}x connections)!`, 'success');
      urlInput.value = '';
      filenameInput.value = '';
      cookieInput.value = '';
      authInput.value = '';
      customRawHeaders.value = '';
      forceMediaCheckbox.checked = false;

      updateTaskElement(data);
    } catch (err) {
      showToast('Error starting download: ' + err.message, 'error');
    }
  });

  // 4. Initial Data
  async function loadInitialData() {
    await Promise.all([refreshStats(), refreshTasks(), refreshFiles()]);
  }

  async function refreshStats() {
    try {
      const res = await fetch('/api/stats');
      if (res.ok) {
        const stats = await res.json();
        updateStatsUI(stats);
      }
    } catch (e) {}
  }

  function updateStatsUI(stats) {
    if (stats.memory) {
      ramVal.textContent = stats.memory.alloc_formatted;
    }
    if (stats.disk) {
      diskVal.textContent = `${stats.disk.used_formatted} / ${stats.disk.total_formatted}`;
      const pct = Math.min(Math.round(stats.disk.used_percent), 100);
      diskBar.style.width = pct + '%';

      if (pct > 85) {
        diskBar.style.background = 'linear-gradient(135deg, #ef4444 0%, #dc2626 100%)';
      } else if (pct > 70) {
        diskBar.style.background = 'linear-gradient(135deg, #f59e0b 0%, #d97706 100%)';
      } else {
        diskBar.style.background = 'var(--accent-gradient)';
      }
    }
  }

  async function refreshTasks() {
    try {
      const res = await fetch('/api/tasks');
      if (res.ok) {
        const tasks = await res.json();
        activeTasks.clear();
        for (const t of tasks) {
          if (t.status === 'pending' || t.status === 'downloading') {
            updateTaskElement(t);
          }
        }
        updateTaskCounts();
      }
    } catch (e) {}
  }

  async function refreshFiles() {
    try {
      const res = await fetch('/api/files');
      if (res.ok) {
        cachedFiles = await res.json();
        renderFiles(cachedFiles);
      }
    } catch (e) {}
  }

  // 5. SSE Real-Time Connection
  function connectSSE() {
    const evtSource = new EventSource('/api/events');

    evtSource.onmessage = (e) => {
      try {
        const task = JSON.parse(e.data);
        handleSSEUpdate(task);
      } catch (err) {}
    };

    evtSource.onerror = () => {
      evtSource.close();
      setTimeout(connectSSE, 3000);
    };
  }

  function handleSSEUpdate(task) {
    if (!task) return;

    if (task.status === 'deleted') {
      refreshFiles();
      refreshStats();
      return;
    }

    if (task.status === 'completed') {
      removeTaskElement(task.id);
      showToast(`Finished: ${task.filename}`, 'success');
      refreshFiles();
      refreshStats();
      return;
    }

    if (task.status === 'failed') {
      removeTaskElement(task.id);
      showToast(`Failed: ${task.error_message || 'Download error'}`, 'error');
      refreshStats();
      return;
    }

    if (task.status === 'cancelled') {
      removeTaskElement(task.id);
      showToast(`Cancelled: ${task.filename}`, 'info');
      refreshStats();
      return;
    }

    updateTaskElement(task);
    updateTaskCounts();
  }

  // 6. Active Tasks UI
  function updateTaskElement(task) {
    activeTasks.set(task.id, task);

    let el = document.getElementById(`task-${task.id}`);
    if (!el) {
      el = document.createElement('div');
      el.id = `task-${task.id}`;
      el.className = 'task-item';
      activeTasksList.appendChild(el);
    }

    const sourceBadgeClass = task.source_type === 'gdrive' ? 'badge-gdrive' : (task.source_type === 'media' ? 'badge-media' : 'badge-direct');
    const sourceLabel = task.source_type === 'gdrive' ? 'Google Drive' : (task.source_type === 'media' ? 'Media' : 'Direct Link');
    const percent = Math.min(Math.max(task.progress_percent || 0, 0), 100).toFixed(1);
    const speed = task.speed_formatted || '0 B/s';
    const downloaded = task.downloaded_formatted || '0 B';
    const total = task.total_formatted || 'Unknown';
    const etaText = task.eta_seconds > 0 ? formatETA(task.eta_seconds) : '';
    const connsBadge = task.connections ? `<span class="badge-conns">${task.connections}x Conns</span>` : '';

    el.innerHTML = `
      <div class="task-header">
        <div class="task-title-group">
          <span class="task-source-badge ${sourceBadgeClass}">${sourceLabel}</span>
          ${connsBadge}
          <span class="task-filename" title="${escapeHtml(task.filename)}">${escapeHtml(task.filename)}</span>
        </div>
        <button class="btn-cancel" onclick="cancelTask('${task.id}')">Cancel</button>
      </div>

      <div class="task-progress-track">
        <div class="task-progress-fill" style="width: ${percent}%"></div>
      </div>

      <div class="task-meta-row">
        <span><strong>${percent}%</strong> &nbsp;•&nbsp; ${downloaded} / ${total}</span>
        <span class="task-speed">${speed} ${etaText ? '• ETA: ' + etaText : ''}</span>
      </div>
    `;

    updateTaskCounts();
  }

  function removeTaskElement(taskId) {
    activeTasks.delete(taskId);
    const el = document.getElementById(`task-${taskId}`);
    if (el) el.remove();
    updateTaskCounts();
  }

  function updateTaskCounts() {
    const count = activeTasks.size;
    activeTasksCount.textContent = count;
    activeCountBadge.textContent = `${count} Active`;
    if (tasksEmpty) {
      tasksEmpty.style.display = count === 0 ? 'flex' : 'none';
    }
  }

  // 7. Render Files List (Direct Downloadable only)
  function renderFiles(files) {
    if (!files || files.length === 0) {
      filesList.innerHTML = '';
      filesList.appendChild(filesEmpty);
      filesEmpty.style.display = 'flex';
      return;
    }

    filesEmpty.style.display = 'none';
    filesList.innerHTML = '';

    const query = searchInput.value.toLowerCase().trim();
    const filtered = query ? files.filter(f => f.name.toLowerCase().includes(query)) : files;

    if (filtered.length === 0) {
      filesList.innerHTML = `<div class="empty-state"><p>No files match "${escapeHtml(query)}"</p></div>`;
      return;
    }

    filtered.forEach(file => {
      const row = document.createElement('div');
      row.className = 'file-row';
      const icon = getFileIcon(file.extension, file.mime_type);

      row.innerHTML = `
        <div class="file-left">
          <div class="file-icon">${icon}</div>
          <div class="file-details">
            <div class="file-name" title="${escapeHtml(file.name)}">${escapeHtml(file.name)}</div>
            <div class="file-subtext">
              <span>${file.size_formatted}</span>
              <span>•</span>
              <span class="file-expiry">Expires in: ${file.expires_in}</span>
            </div>
          </div>
        </div>

        <div class="file-actions">
          <a class="btn-action btn-action-primary" href="${file.download_url}" download="${escapeHtml(file.name)}">⚡ Direct Download</a>
          <button class="btn-action" onclick="copyFileLink('${file.download_url}')">📋 Copy Link</button>
          <button class="btn-action btn-action-delete" onclick="deleteFile('${escapeHtml(file.name)}')">🗑️</button>
        </div>
      `;

      filesList.appendChild(row);
    });
  }

  // 8. Search Filter
  searchInput.addEventListener('input', () => {
    renderFiles(cachedFiles);
  });

  // 9. Clear All Files Handler
  clearAllBtn.addEventListener('click', async () => {
    if (!confirm('Are you sure you want to purge all downloaded files from server disk?')) return;

    try {
      const res = await fetch('/api/files', { method: 'DELETE' });
      if (res.ok) {
        showToast('All files purged successfully', 'success');
        refreshFiles();
        refreshStats();
      }
    } catch (e) {
      showToast('Error purging files: ' + e.message, 'error');
    }
  });

  // 10. Global Actions
  window.cancelTask = async (taskId) => {
    try {
      const res = await fetch(`/api/cancel/${taskId}`, { method: 'POST' });
      if (res.ok) {
        showToast('Download cancellation requested', 'info');
      }
    } catch (e) {}
  };

  window.deleteFile = async (filename) => {
    if (!confirm(`Delete "${filename}"?`)) return;

    try {
      const res = await fetch(`/api/files/${encodeURIComponent(filename)}`, { method: 'DELETE' });
      if (res.ok) {
        showToast(`Deleted ${filename}`, 'info');
        refreshFiles();
        refreshStats();
      }
    } catch (e) {
      showToast('Error deleting file: ' + e.message, 'error');
    }
  };

  window.copyFileLink = (relUrl) => {
    const fullURL = window.location.origin + relUrl;
    navigator.clipboard.writeText(fullURL).then(() => {
      showToast('Direct download link copied to clipboard!', 'info');
    });
  };

  // Helper Utilities
  function formatETA(sec) {
    if (sec <= 0) return '';
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    if (m >= 60) {
      const h = Math.floor(m / 60);
      return `${h}h ${m % 60}m`;
    }
    return `${m}m ${s}s`;
  }

  function getFileIcon(ext, mime) {
    if (['.zip', '.tar', '.gz', '.7z', '.rar', '.xz', '.bz2', '.iso'].includes(ext)) return '📦';
    if (['.mp4', '.mkv', '.webm', '.avi', '.mov', '.ts', '.flv'].includes(ext)) return '🎬';
    if (['.mp3', '.m4a', '.flac', '.wav', '.ogg', '.aac'].includes(ext)) return '🎵';
    if (['.pdf', '.epub', '.doc', '.docx', '.txt'].includes(ext)) return '📄';
    if (['.exe', '.apk', '.deb', '.rpm', '.dmg', '.msi'].includes(ext)) return '⚙️';
    return '📁';
  }

  function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/[&<>"']/g, (m) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;',
      "'": '&#039;',
    }[m]));
  }

  function showToast(msg, type = 'info') {
    const container = document.getElementById('toast-container');
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    const icon = type === 'success' ? '✅' : (type === 'error' ? '❌' : 'ℹ️');
    toast.innerHTML = `<span>${icon}</span> <span>${escapeHtml(msg)}</span>`;
    container.appendChild(toast);

    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transform = 'translateY(10px)';
      toast.style.transition = 'all 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, 3500);
  }
});
