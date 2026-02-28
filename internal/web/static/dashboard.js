(function () {
  "use strict"

  // ── State ───────────────────────────────────────────────────────
  var state = {
    tasks: [],
    projects: [],
    selectedTaskId: null,
    activeView: "agents",
    projectFilter: "",
    authToken: readAuthTokenFromURL(),
    terminal: null,
    terminalWs: null,
    previewStream: null,
    fitAddon: null,
    terminalResizeObserver: null,
    terminalFontSize: 14,
    terminalFullscreen: false,
    chatMode: null,
    chatModeOverride: null,
    sessionMap: {},  // menuSession.id → menuSession (from SSE menu events)
    workspacesSubTab: "workspaces",  // "workspaces" | "templates"
  }

  // ── Status metadata ─────────────────────────────────────────────
  var AGENT_STATUS_META = {
    thinking: { icon: "\u25CF", label: "Thinking", color: "var(--orange)" },
    waiting:  { icon: "\u25D0", label: "Input needed", color: "var(--orange)" },
    running:  { icon: "\u27F3", label: "Running", color: "var(--blue)" },
    idle:     { icon: "\u25CB", label: "Idle", color: "var(--text-dim)" },
    error:    { icon: "\u2715", label: "Error", color: "var(--red)" },
    complete: { icon: "\u2713", label: "Complete", color: "var(--green)" },
  }

  var TASK_STATUS_COLORS = {
    backlog:  "var(--text-dim)",
    planning: "var(--phase-plan)",
    running:  "var(--phase-execute)",
    review:   "var(--phase-review)",
    done:     "var(--phase-done)",
  }

  var PHASE_COLORS_HEX = {
    brainstorm: "#c084fc",
    plan: "#8b8cf8",
    execute: "#e8a932",
    review: "#4ca8e8",
    done: "#2dd4a0",
  }

  var PHASES = ["brainstorm", "plan", "execute", "review"]
  var PHASE_DOT_LABELS = { brainstorm: "B", plan: "P", execute: "E", review: "R" }

  // ── Live session helpers ────────────────────────────────────────────
  function getActiveSessionForTask(task) {
    if (!task.sessions) return null
    for (var i = task.sessions.length - 1; i >= 0; i--) {
      if (task.sessions[i].status === "active" && task.sessions[i].claudeSessionId) {
        return state.sessionMap[task.sessions[i].claudeSessionId] || null
      }
    }
    return null
  }

  // Return the session.Instance ID to use for the messages API.
  // Prefers the active hub session's claudeSessionId; falls back to
  // task.tmuxSession for container-backed tasks.
  function getSessionIdForMessages(task) {
    if (!task) return null
    if (task.sessions) {
      for (var i = task.sessions.length - 1; i >= 0; i--) {
        if (task.sessions[i].status === "active" && task.sessions[i].claudeSessionId) {
          return task.sessions[i].claudeSessionId
        }
      }
    }
    return task.tmuxSession || null
  }

  function mapSessionStatus(sessionStatus) {
    switch (sessionStatus) {
      case "running": return "running"
      case "waiting": return "waiting"
      case "idle":    return "idle"
      case "error":   return "error"
      case "starting": return "thinking"
      default:        return "idle"
    }
  }

  // Return the effective agent status for a task. If a live session exists
  // in the sessionMap, use its real-time status. Otherwise fall back to the
  // task's persisted agentStatus — but treat transient states ("thinking",
  // "running") as "idle" when no live session backs them, since the agent
  // is no longer active.
  function effectiveAgentStatus(task) {
    var live = getActiveSessionForTask(task)
    if (live) return mapSessionStatus(live.status)
    var s = task.agentStatus || ""
    if (s === "thinking" || s === "running") return "idle"
    return s || "idle"
  }

  // ── Auth ──────────────────────────────────────────────────────────
  function readAuthTokenFromURL() {
    var params = new URLSearchParams(window.location.search || "")
    return String(params.get("token") || "").trim()
  }

  function apiPathWithToken(path) {
    if (!state.authToken) return path
    var url = new URL(path, window.location.origin)
    url.searchParams.set("token", state.authToken)
    return url.pathname + url.search
  }

  function authHeaders() {
    var h = { Accept: "application/json" }
    if (state.authToken) h.Authorization = "Bearer " + state.authToken
    return h
  }

  // ── Helpers: safe DOM construction ────────────────────────────────
  function el(tag, className, textContent) {
    var node = document.createElement(tag)
    if (className) node.className = className
    if (textContent != null) node.textContent = textContent
    return node
  }

  function clearChildren(parent) {
    while (parent.firstChild) parent.removeChild(parent.firstChild)
  }

  // ── Data fetching ─────────────────────────────────────────────────
  function fetchTasks() {
    return fetch(apiPathWithToken("/api/tasks"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("tasks fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        state.tasks = data.tasks || []
        renderTaskList()
        updateAgentCount()
        renderTopBar()
        if (state.selectedTaskId) {
          var task = findTask(state.selectedTaskId)
          if (task) renderRightPanel(task)
        }
      })
      .catch(function (err) {
        console.error("fetchTasks:", err)
        state.tasks = []
        renderTaskList()
      })
  }

  function fetchProjects() {
    return fetch(apiPathWithToken("/api/projects"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("projects fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        state.projects = data.projects || []
        renderFilterBar()
      })
      .catch(function (err) {
        console.error("fetchProjects:", err)
        state.projects = []
        renderFilterBar()
      })
  }

  // Fetch menu snapshot and populate sessionMap so live terminal
  // connections and messages can resolve session.Instance IDs to
  // their tmux session names and project paths.
  function fetchMenuData() {
    return fetch(apiPathWithToken("/api/menu"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("menu fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        var items = data && data.items ? data.items : []
        var map = {}
        for (var i = 0; i < items.length; i++) {
          if (items[i].type === "session" && items[i].session) {
            map[items[i].session.id] = items[i].session
          }
        }
        state.sessionMap = map
        // Re-render so agent status badges reflect live session data
        // (resolves race where tasks render before sessionMap is populated).
        renderTaskList()
        if (state.selectedTaskId) {
          var task = findTask(state.selectedTaskId)
          if (task) renderRightPanel(task)
        }
      })
      .catch(function (err) {
        console.error("fetchMenuData:", err)
      })
  }

  function findTask(id) {
    for (var i = 0; i < state.tasks.length; i++) {
      if (state.tasks[i].id === id) return state.tasks[i]
    }
    return null
  }

  function getCardBorderColor(task) {
    if (effectiveAgentStatus(task) === "waiting") return "var(--orange)"
    return TASK_STATUS_COLORS[task.status] || "var(--text-dim)"
  }


  function setConnectionState(s) {
    var dot = document.getElementById("sidebar-status-dot")
    if (dot) {
      dot.className = "sidebar-status-dot"
      if (s === "connected") dot.classList.add("connected")
      else if (s === "connecting" || s === "reconnecting") dot.classList.add("connecting")
      else if (s === "error" || s === "closed") dot.classList.add("error")
    }
  }

  function updateAgentCount() {
    var countEl = document.getElementById("sidebar-agent-count")
    var active = 0
    for (var i = 0; i < state.tasks.length; i++) {
      if (state.tasks[i].status !== "done") active++
    }
    if (countEl) countEl.textContent = active + " agent" + (active !== 1 ? "s" : "")
  }

  // ── ConnectionManager (WebSocket with auto-reconnect) ────────────
  function ConnectionManager(url) {
    this.url = url
    this.state = "disconnected"
    this.ws = null
    this.lastEventAt = 0
    this.reconnectAttempts = 0
    this.subscriptions = {}
    this._listeners = { stateChange: [], reconnect: [] }
    this._staleTimer = null
    this._reconnectTimer = null
    this._pongTimer = null
    this._awaitingPong = false

    // Config
    this.staleThresholdMs = 45000
    this.pongTimeoutMs = 2000
    this.baseDelayMs = 1000
    this.maxDelayMs = 30000
    this.maxAttempts = 10
  }

  ConnectionManager.prototype.connect = function () {
    var self = this
    var wasConnected = this.state === "connected" || this.state === "reconnecting"
    if (this.ws) {
      try { this.ws.close() } catch (_) { /* ignore */ }
    }
    if (wasConnected) {
      this._setState("reconnecting")
    }
    var ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = function () {
      var isReconnect = self.reconnectAttempts > 0
      self.reconnectAttempts = 0
      self.lastEventAt = Date.now()
      self._setState("connected")
      self._resubscribeAll()
      self._startStaleCheck()
      // Fire reconnect listeners after connection is fully established
      if (isReconnect) {
        for (var i = 0; i < self._listeners.reconnect.length; i++) {
          try { self._listeners.reconnect[i]() } catch (_) { /* ignore */ }
        }
      }
    }

    ws.onmessage = function (e) {
      self.lastEventAt = Date.now()
      try {
        var msg = JSON.parse(e.data)
        self._handleMessage(msg)
      } catch (err) {
        console.error("ConnectionManager: bad message", err)
      }
    }

    ws.onclose = function () {
      self.ws = null
      self._stopStaleCheck()
      if (self.state !== "disconnected") {
        self._setState("reconnecting")
        self._scheduleReconnect()
      }
    }

    ws.onerror = function () {
      // onclose will fire after onerror, so reconnect logic lives there
    }

    this._setupVisibility()
  }

  ConnectionManager.prototype._handleMessage = function (msg) {
    if (!msg || !msg.type) return

    if (msg.type === "pong") {
      this._awaitingPong = false
      if (this._pongTimer) {
        clearTimeout(this._pongTimer)
        this._pongTimer = null
      }
      return
    }

    if (msg.type === "heartbeat") {
      // heartbeat keeps connection alive; lastEventAt already updated
      return
    }

    // Event or snapshot: dispatch to channel subscribers
    var channel = msg.channel || ""
    if (channel && this.subscriptions[channel]) {
      var handlers = this.subscriptions[channel]
      for (var i = 0; i < handlers.length; i++) {
        try { handlers[i](msg) } catch (err) {
          console.error("ConnectionManager: handler error on " + channel, err)
        }
      }
    }
  }

  ConnectionManager.prototype.subscribe = function (channel, handler) {
    if (!this.subscriptions[channel]) {
      this.subscriptions[channel] = []
    }
    this.subscriptions[channel].push(handler)

    // Send subscribe message if already connected
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "subscribe", channel: channel }))
    }

    var self = this
    return function () {
      var arr = self.subscriptions[channel]
      if (!arr) return
      var idx = arr.indexOf(handler)
      if (idx !== -1) arr.splice(idx, 1)
      if (arr.length === 0) delete self.subscriptions[channel]
    }
  }

  ConnectionManager.prototype._resubscribeAll = function () {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    var channels = Object.keys(this.subscriptions)
    for (var i = 0; i < channels.length; i++) {
      try {
        this.ws.send(JSON.stringify({ type: "subscribe", channel: channels[i] }))
      } catch (_) { /* ws may have closed mid-loop */ }
    }
  }

  ConnectionManager.prototype._scheduleReconnect = function () {
    var self = this
    if (this._reconnectTimer) return
    if (this.reconnectAttempts >= this.maxAttempts) {
      self._setState("disconnected")
      return
    }

    var delay = Math.min(
      this.baseDelayMs * Math.pow(2, this.reconnectAttempts),
      this.maxDelayMs
    )
    // Jitter: multiply by (0.5 + random * 0.5) so delay is between 50%-100% of computed value
    delay = Math.floor(delay * (0.5 + Math.random() * 0.5))
    this.reconnectAttempts++

    this._reconnectTimer = setTimeout(function () {
      self._reconnectTimer = null
      self.connect()
    }, delay)
  }

  ConnectionManager.prototype._startStaleCheck = function () {
    var self = this
    this._stopStaleCheck()
    this._staleTimer = setInterval(function () {
      if (self.state !== "connected") return
      var elapsed = Date.now() - self.lastEventAt
      if (elapsed > self.staleThresholdMs) {
        // Connection appears stale — attempt recovery via ping
        self._checkWithPing()
      }
    }, 15000)
  }

  ConnectionManager.prototype._stopStaleCheck = function () {
    if (this._staleTimer) {
      clearInterval(this._staleTimer)
      this._staleTimer = null
    }
  }

  ConnectionManager.prototype._setupVisibility = function () {
    var self = this
    // Only set up once
    if (this._visibilityBound) return
    this._visibilityBound = true

    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState === "visible" && self.state === "connected") {
        self._checkWithPing()
      }
    })
  }

  ConnectionManager.prototype._checkWithPing = function () {
    var self = this
    if (this._awaitingPong) return
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return

    this._awaitingPong = true
    try {
      this.ws.send(JSON.stringify({ type: "ping" }))
    } catch (_) {
      this._awaitingPong = false
      return
    }

    this._pongTimer = setTimeout(function () {
      self._pongTimer = null
      if (self._awaitingPong) {
        // No pong received in time — connection is dead
        self._awaitingPong = false
        if (self.ws) {
          try { self.ws.close() } catch (_) { /* ignore */ }
        }
      }
    }, this.pongTimeoutMs)
  }

  ConnectionManager.prototype._setState = function (newState) {
    if (this.state === newState) return
    var oldState = this.state
    this.state = newState
    for (var i = 0; i < this._listeners.stateChange.length; i++) {
      try { this._listeners.stateChange[i](newState, oldState) } catch (_) { /* ignore */ }
    }
  }

  ConnectionManager.prototype.on = function (event, handler) {
    if (this._listeners[event]) {
      this._listeners[event].push(handler)
    }
  }

  ConnectionManager.prototype.disconnect = function () {
    this._setState("disconnected")
    this._stopStaleCheck()
    if (this._reconnectTimer) {
      clearTimeout(this._reconnectTimer)
      this._reconnectTimer = null
    }
    if (this._pongTimer) {
      clearTimeout(this._pongTimer)
      this._pongTimer = null
    }
    if (this.ws) {
      try { this.ws.close() } catch (_) { /* ignore */ }
      this.ws = null
    }
  }

  // ── Sidebar ───────────────────────────────────────────────────────
  function renderSidebar() {
    var icons = document.querySelectorAll(".sidebar-icon[data-view]")
    for (var i = 0; i < icons.length; i++) {
      if (icons[i].dataset.view === state.activeView) {
        icons[i].classList.add("sidebar-icon--active")
      } else {
        icons[i].classList.remove("sidebar-icon--active")
      }
    }
  }

  function handleSidebarClick(e) {
    var btn = e.currentTarget
    var view = btn.dataset.view
    if (!view) return
    state.activeView = view
    state.chatModeOverride = null
    if (view !== "workspaces") state.workspacesSubTab = "workspaces"
    renderSidebar()
    renderTopBar()
    renderView()
    renderChatBar()
  }

  // ── Top bar ──────────────────────────────────────────────────────
  function renderTopBar() {
    var leftEl = document.getElementById("top-bar-left")
    var rightEl = document.getElementById("top-bar-right")
    if (!leftEl || !rightEl) return

    clearChildren(leftEl)
    clearChildren(rightEl)

    // View label
    var viewLabel = state.activeView
    if (state.activeView === "workspaces" && state.workspacesSubTab === "templates") {
      viewLabel = "workspaces / templates"
    }
    leftEl.appendChild(el("span", "top-bar-view", viewLabel))

    // Breadcrumb for selected task
    var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null
    if (task && (state.activeView === "agents" || state.activeView === "kanban")) {
      leftEl.appendChild(el("span", "top-bar-sep", "/"))
      leftEl.appendChild(el("span", "top-bar-project", task.project || "\u2014"))
      leftEl.appendChild(el("span", "top-bar-sep", "/"))
      leftEl.appendChild(el("span", "top-bar-task-id", task.id))
      leftEl.appendChild(el("span", "top-bar-sep", "/"))

      var phasePill = el("span", "top-bar-phase", task.phase || "\u2014")
      var phaseColor = PHASE_COLORS_HEX[task.phase] || "#4a5368"
      phasePill.style.background = phaseColor + "20"
      phasePill.style.color = phaseColor
      leftEl.appendChild(phasePill)
    }

    // Right side: action buttons when task with tmux is selected
    var isMobile = window.innerWidth < 768
    if (task && task.tmuxSession && !isMobile) {
      var attachBtn = el("button", "top-bar-action top-bar-action--attach", "\u25B6 Attach")
      attachBtn.title = "Attach to " + task.tmuxSession
      attachBtn.addEventListener("click", function () {
        window.open("/terminal?session=" + encodeURIComponent(task.tmuxSession), "_blank")
      })
      rightEl.appendChild(attachBtn)

      var sshBtn = el("button", "top-bar-action", "\u229E SSH")
      sshBtn.title = "SSH — coming soon"
      sshBtn.disabled = true
      sshBtn.style.opacity = "0.4"
      sshBtn.style.cursor = "default"
      rightEl.appendChild(sshBtn)

      var ideBtn = el("button", "top-bar-action", "\u27E8\u27E9 IDE")
      ideBtn.title = "IDE — coming soon"
      ideBtn.disabled = true
      ideBtn.style.opacity = "0.4"
      ideBtn.style.cursor = "default"
      rightEl.appendChild(ideBtn)
    }

    // Agent count indicator
    var activeCount = 0
    for (var i = 0; i < state.tasks.length; i++) {
      var t = state.tasks[i]
      if (t.tmuxSession && t.status !== "done" && t.status !== "backlog") {
        activeCount++
      }
    }
    var countSpan = el("span", "top-bar-agent-indicator")
    var dot = el("span", "top-bar-agent-dot")
    countSpan.appendChild(dot)
    countSpan.appendChild(document.createTextNode(" " + activeCount))
    rightEl.appendChild(countSpan)
  }

  // ── View switching ─────────────────────────────────────────────────
  var VIEW_PLACEHOLDERS = {
    kanban: { icon: "\u25A6", title: "Kanban Board", desc: "Board view with columns \u2014 coming soon." },
    workspaces: { icon: "\u25A3", title: "Workspaces", desc: "Dev environment management \u2014 coming soon." },
    brainstorm: { icon: "\u25C7", title: "Brainstorm", desc: "Pre-project ideation \u2014 coming soon." },
  }

  // ── Conductor log type styles ────────────────────────────────────
  var CONDUCTOR_LOG_STYLES = {
    check:  { icon: "\u2713", color: "var(--green)" },
    action: { icon: "\u2192", color: "var(--accent)" },
    alert:  { icon: "\u26A0", color: "var(--red)" },
    route:  { icon: "\u25C8", color: "var(--purple)" },
    spawn:  { icon: "\u2295", color: "var(--blue)" },
    ask:    { icon: "\u25D0", color: "var(--orange)" },
  }

  function renderView() {
    // Clean up any open popups from previous view
    closeSlashPalette()
    closeModeMenu()
    // Stop workspace polling when switching away
    if (state.activeView !== "workspaces") stopWorkspacePolling()

    var panels = document.getElementById("panels")
    var placeholder = document.getElementById("view-placeholder")
    var conductorEl = document.getElementById("conductor-view")
    var kanbanEl = document.getElementById("kanban-view")
    var workspacesEl = document.getElementById("workspaces-view")
    var chatBar = document.getElementById("chat-bar")

    // Hide all non-active views
    var hideAll = function () {
      if (panels) panels.style.display = "none"
      if (placeholder) placeholder.style.display = "none"
      if (conductorEl) conductorEl.style.display = "none"
      if (kanbanEl) kanbanEl.style.display = "none"
      if (workspacesEl) workspacesEl.style.display = "none"
    }

    if (state.activeView === "agents") {
      hideAll()
      if (panels) panels.style.display = ""
      return
    }

    hideAll()

    // Conductor view
    if (state.activeView === "conductor") {
      renderConductorView()
      return
    }

    // Kanban view
    if (state.activeView === "kanban") {
      renderKanbanView()
      return
    }

    // Workspaces view
    if (state.activeView === "workspaces") {
      renderWorkspacesView()
      return
    }

    // Create placeholder if needed
    if (!placeholder) {
      placeholder = el("div", "view-placeholder")
      placeholder.id = "view-placeholder"
      var parent = panels ? panels.parentNode : document.querySelector(".main-content")
      if (parent && chatBar) {
        parent.insertBefore(placeholder, chatBar)
      } else if (parent) {
        parent.appendChild(placeholder)
      }
    }

    clearChildren(placeholder)
    placeholder.style.display = ""

    var info = VIEW_PLACEHOLDERS[state.activeView] || { icon: "?", title: state.activeView, desc: "Coming soon." }
    placeholder.appendChild(el("div", "view-placeholder-icon", info.icon))
    placeholder.appendChild(el("div", "view-placeholder-title", info.title))
    placeholder.appendChild(el("div", "view-placeholder-text", info.desc))
  }

  // ── Conductor view ───────────────────────────────────────────────
  function renderConductorView() {
    var chatBar = document.getElementById("chat-bar")
    var conductorEl = document.getElementById("conductor-view")

    if (!conductorEl) {
      conductorEl = el("div", "conductor-view")
      conductorEl.id = "conductor-view"
      var parent = document.querySelector(".main-content")
      if (parent && chatBar) {
        parent.insertBefore(conductorEl, chatBar)
      } else if (parent) {
        parent.appendChild(conductorEl)
      }
    }

    clearChildren(conductorEl)
    conductorEl.style.display = ""

    // Fetch conductor status from API (graceful fallback)
    fetch(apiPathWithToken("/api/conductor"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("conductor fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        buildConductorContent(conductorEl, data.conductor || null, data.log || [])
      })
      .catch(function () {
        // No conductor API yet — render empty state
        buildConductorContent(conductorEl, null, [])
      })
  }

  function buildConductorContent(container, conductor, log) {
    clearChildren(container)

    if (!conductor) {
      // Empty / not configured state
      var emptyCard = el("div", "conductor-identity")

      var avatar = el("div", "conductor-avatar", "\u25CE")
      emptyCard.appendChild(avatar)

      var info = el("div", "conductor-info")
      var nameRow = el("div", "conductor-name", "No conductor running")
      info.appendChild(nameRow)

      var desc = el("div", "conductor-desc", "Start a conductor session to orchestrate agents")
      info.appendChild(desc)

      emptyCard.appendChild(info)
      container.appendChild(emptyCard)

      // Log header even when empty
      container.appendChild(el("div", "conductor-log-header", "Activity Log"))

      var emptyLog = el("div", "conductor-log-entry")
      var emptyMsg = el("span", "conductor-log-msg", "No activity yet")
      emptyMsg.style.color = "var(--text-dim)"
      emptyLog.appendChild(emptyMsg)
      container.appendChild(emptyLog)
      return
    }

    // ── Identity card ──────────────────────────────────────────────
    var card = el("div", "conductor-identity")

    var avatar = el("div", "conductor-avatar", "\u25CE")
    card.appendChild(avatar)

    var infoDiv = el("div", "conductor-info")

    // Name + status
    var nameRow = el("div", "conductor-name")
    nameRow.textContent = conductor.name || "conductor"
    var statusSpan = el("span",
      "conductor-status conductor-status--" + (conductor.status === "connected" ? "connected" : "disconnected"),
      "\u25CF " + (conductor.status || "disconnected")
    )
    nameRow.appendChild(document.createTextNode(" "))
    nameRow.appendChild(statusSpan)
    infoDiv.appendChild(nameRow)

    // Description line
    var descLine = el("div", "conductor-desc")
    descLine.textContent = "Separate Claude instance \u00B7 tmux: " + (conductor.tmuxSession || "n/a")
    infoDiv.appendChild(descLine)

    // Stats row
    var stats = el("div", "conductor-stats")
    stats.appendChild(el("span", "", "heartbeat: " + (conductor.heartbeatInterval || "n/a")))
    var autoApprove = conductor.autoApprove
    if (autoApprove && autoApprove.length) {
      stats.appendChild(el("span", "", "auto-approve: " + autoApprove.join(", ")))
    }
    if (conductor.monitoredSessions != null) {
      stats.appendChild(el("span", "", "monitoring: " + conductor.monitoredSessions + " sessions"))
    }
    infoDiv.appendChild(stats)

    card.appendChild(infoDiv)
    container.appendChild(card)

    // ── Activity log ───────────────────────────────────────────────
    container.appendChild(el("div", "conductor-log-header", "Activity Log"))

    if (!log || !log.length) {
      var noLog = el("div", "conductor-log-entry")
      var noMsg = el("span", "conductor-log-msg", "No recent activity")
      noMsg.style.color = "var(--text-dim)"
      noLog.appendChild(noMsg)
      container.appendChild(noLog)
      return
    }

    for (var i = 0; i < log.length; i++) {
      var entry = log[i]
      var style = CONDUCTOR_LOG_STYLES[entry.type] || CONDUCTOR_LOG_STYLES.check

      var row = el("div", "conductor-log-entry")

      var time = el("span", "conductor-log-time", entry.time || "")
      row.appendChild(time)

      var icon = el("span", "conductor-log-icon", style.icon)
      icon.style.color = style.color
      row.appendChild(icon)

      var msg = el("span", "conductor-log-msg", entry.msg || "")
      row.appendChild(msg)

      container.appendChild(row)
    }
  }

  // ── Kanban view ──────────────────────────────────────────────────
  var KANBAN_COLUMNS = ["backlog", "planning", "running", "review", "done"]
  var KANBAN_STATUS_META = {
    backlog:  { icon: "\u25CB", color: "var(--text-dim)" },
    planning: { icon: "\u25C8", color: "var(--purple)" },
    running:  { icon: "\u27F3", color: "var(--accent)" },
    review:   { icon: "\u25CE", color: "var(--blue)" },
    done:     { icon: "\u2713", color: "var(--green)" },
  }

  var kanbanGroupByProject = true

  function renderKanbanView() {
    var chatBar = document.getElementById("chat-bar")
    var kanbanEl = document.getElementById("kanban-view")

    if (!kanbanEl) {
      kanbanEl = el("div", "kanban-view")
      kanbanEl.id = "kanban-view"
      var parent = document.querySelector(".main-content")
      if (parent && chatBar) {
        parent.insertBefore(kanbanEl, chatBar)
      } else if (parent) {
        parent.appendChild(kanbanEl)
      }
    }

    clearChildren(kanbanEl)
    kanbanEl.style.display = ""

    // ── Filter bar ──────────────────────────────────────────────
    var filterBar = el("div", "kanban-filter-bar")

    // "All" button
    var allBtn = el("button",
      "kanban-filter-btn" + (state.projectFilter === "" ? " kanban-filter-btn--active" : ""),
      "All (" + state.tasks.length + ")"
    )
    allBtn.addEventListener("click", function () {
      state.projectFilter = ""
      renderKanbanView()
      renderTopBar()
    })
    filterBar.appendChild(allBtn)

    // Per-project buttons
    var projects = state.projects || []
    for (var p = 0; p < projects.length; p++) {
      ;(function (proj) {
        var active = state.projectFilter === proj.name
        var btn = el("button",
          "kanban-filter-btn" + (active ? " kanban-filter-btn--active" : ""),
          proj.name
        )
        btn.addEventListener("click", function () {
          state.projectFilter = active ? "" : proj.name
          renderKanbanView()
          renderTopBar()
        })
        filterBar.appendChild(btn)
      })(projects[p])
    }

    // Spacer
    filterBar.appendChild(el("div", "kanban-filter-spacer"))

    // Group toggle
    var groupBtn = el("button",
      "kanban-group-btn" + (kanbanGroupByProject ? " kanban-group-btn--active" : "")
    )
    groupBtn.appendChild(document.createTextNode((kanbanGroupByProject ? "\u25A4 " : "\u25A5 ") + "Group"))
    groupBtn.addEventListener("click", function () {
      kanbanGroupByProject = !kanbanGroupByProject
      renderKanbanView()
    })
    filterBar.appendChild(groupBtn)

    kanbanEl.appendChild(filterBar)

    // ── Columns ─────────────────────────────────────────────────
    var columnsContainer = el("div", "kanban-columns")

    // Filter tasks
    var tasks = state.tasks || []
    if (state.projectFilter) {
      tasks = tasks.filter(function (t) { return t.project === state.projectFilter })
    }

    for (var c = 0; c < KANBAN_COLUMNS.length; c++) {
      var colName = KANBAN_COLUMNS[c]
      var colMeta = KANBAN_STATUS_META[colName] || KANBAN_STATUS_META.backlog

      // Map task status to kanban column
      var colTasks = tasks.filter(function (t) {
        return mapTaskToKanbanColumn(t) === colName
      })

      var column = el("div", "kanban-column")

      // Column header
      var header = el("div", "kanban-column-header")
      header.style.color = colMeta.color
      var headerIcon = el("span", "", colMeta.icon)
      header.appendChild(headerIcon)
      header.appendChild(document.createTextNode(" " + colName + " "))
      var countSpan = el("span", "kanban-column-count", String(colTasks.length))
      header.appendChild(countSpan)
      column.appendChild(header)

      // Column body
      var body = el("div", "kanban-column-body")

      if (colTasks.length === 0) {
        body.appendChild(el("div", "kanban-empty", "\u2014"))
      } else if (kanbanGroupByProject) {
        // Group by project
        var projectSet = []
        for (var i = 0; i < colTasks.length; i++) {
          if (projectSet.indexOf(colTasks[i].project) === -1) {
            projectSet.push(colTasks[i].project)
          }
        }
        for (var gi = 0; gi < projectSet.length; gi++) {
          var projName = projectSet[gi]
          body.appendChild(el("div", "kanban-project-group", "\u25A3 " + projName))
          for (var ti = 0; ti < colTasks.length; ti++) {
            if (colTasks[ti].project === projName) {
              body.appendChild(createKanbanCard(colTasks[ti], colMeta))
            }
          }
        }
      } else {
        for (var j = 0; j < colTasks.length; j++) {
          body.appendChild(createKanbanCard(colTasks[j], colMeta))
        }
      }

      column.appendChild(body)
      columnsContainer.appendChild(column)
    }

    kanbanEl.appendChild(columnsContainer)
  }

  function mapTaskToKanbanColumn(task) {
    var status = task.status || ""
    if (status === "done") return "done"
    if (status === "review") return "review"
    if (status === "running") return "running"
    if (status === "planning") return "planning"
    // Map phase-based tasks
    var phase = task.phase || ""
    if (phase === "done") return "done"
    if (phase === "review") return "review"
    if (phase === "execute") return "running"
    if (phase === "plan") return "planning"
    if (phase === "brainstorm") return "planning"
    return "backlog"
  }

  function createKanbanCard(task, colMeta) {
    var card = el("div", "kanban-card" + (state.selectedTaskId === task.id ? " kanban-card--selected" : ""))
    card.style.borderLeftColor = colMeta.color

    card.addEventListener("click", function () {
      state.selectedTaskId = task.id
      renderKanbanView()
    })

    // Project name
    card.appendChild(el("div", "kanban-card-project", task.project || ""))

    // Description
    card.appendChild(el("div", "kanban-card-desc", task.description || ""))

    // Footer: id + agent status
    var footer = el("div", "kanban-card-footer")
    footer.appendChild(el("span", "kanban-card-id", task.id || ""))

    var kanbanStatus = effectiveAgentStatus(task)
    if (kanbanStatus) {
      var meta = AGENT_STATUS_META[kanbanStatus]
      if (meta) {
        var statusEl = el("span", "kanban-card-status")
        statusEl.style.color = meta.color
        statusEl.textContent = meta.icon
        footer.appendChild(statusEl)
      }
    }

    card.appendChild(footer)
    return card
  }

  // ── Workspaces view ──────────────────────────────────────────────
  var workspacePollInterval = null

  function renderWorkspacesView() {
    var chatBar = document.getElementById("chat-bar")
    var wsEl = document.getElementById("workspaces-view")

    if (!wsEl) {
      wsEl = el("div", "workspaces-view")
      wsEl.id = "workspaces-view"
      var parent = document.querySelector(".main-content")
      if (parent && chatBar) {
        parent.insertBefore(wsEl, chatBar)
      } else if (parent) {
        parent.appendChild(wsEl)
      }
    }

    clearChildren(wsEl)
    wsEl.style.display = ""

    // Render toggle bar
    var toggleBar = el("div", "ws-toggle-bar")
    var wsTab = el("button", "ws-toggle-tab" + (state.workspacesSubTab === "workspaces" ? " ws-toggle-tab--active" : ""), "Workspaces")
    wsTab.addEventListener("click", function () {
      state.workspacesSubTab = "workspaces"
      renderWorkspacesView()
      renderTopBar()
    })
    var tmplTab = el("button", "ws-toggle-tab" + (state.workspacesSubTab === "templates" ? " ws-toggle-tab--active" : ""), "Templates")
    tmplTab.addEventListener("click", function () {
      state.workspacesSubTab = "templates"
      renderWorkspacesView()
      renderTopBar()
    })
    toggleBar.appendChild(wsTab)
    toggleBar.appendChild(tmplTab)
    wsEl.appendChild(toggleBar)

    // Content area
    var contentEl = el("div", "ws-content")
    wsEl.appendChild(contentEl)

    if (state.workspacesSubTab === "templates") {
      stopWorkspacePolling()
      fetchTemplates(contentEl)
    } else {
      // Start polling for workspaces
      stopWorkspacePolling()
      workspacePollInterval = setInterval(function () {
        if (state.activeView === "workspaces" && state.workspacesSubTab === "workspaces") {
          fetchWorkspaces(contentEl)
        }
      }, 5000)
      fetchWorkspaces(contentEl)
    }
  }

  function stopWorkspacePolling() {
    if (workspacePollInterval) {
      clearInterval(workspacePollInterval)
      workspacePollInterval = null
    }
  }

  function fetchWorkspaces(contentEl) {
    fetch(apiPathWithToken("/api/workspaces"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("workspaces fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        buildWorkspacesContent(contentEl, data.workspaces || [])
      })
      .catch(function () {
        // Fallback to projects
        var workspaces = (state.projects || []).map(function (p) {
          return {
            name: p.name,
            desc: p.description || "",
            image: p.image || "",
            containerStatus: p.containerStatus || "not_created",
            path: p.path || "/workspace/" + p.name,
            container: p.container || "",
            activeTasks: 0,
          }
        })
        buildWorkspacesContent(contentEl, workspaces)
      })
  }

  function buildWorkspacesContent(container, workspaces) {
    clearChildren(container)

    if (!workspaces || !workspaces.length) {
      var emptyCard = el("div", "workspace-card")
      emptyCard.appendChild(el("div", "workspace-card-name", "No workspaces configured"))
      emptyCard.appendChild(el("div", "workspace-card-desc", "Add a project to get started"))
      container.appendChild(emptyCard)
    } else {
      for (var i = 0; i < workspaces.length; i++) {
        container.appendChild(createWorkspaceCard(workspaces[i]))
      }
    }

    var provBtn = el("button", "workspace-provision-btn", "+ Add Workspace")
    provBtn.addEventListener("click", openAddProjectModal)
    container.appendChild(provBtn)
  }

  function createWorkspaceCard(ws) {
    var card = el("div", "workspace-card")
    var top = el("div", "workspace-card-top")
    top.appendChild(el("span", "workspace-card-name", ws.name))

    var actions = el("div", "workspace-card-actions")
    var isRunning = ws.containerStatus === "running"
    var isStopped = ws.containerStatus === "stopped"

    var badge = el("span", "workspace-badge")
    badge.style.color = isRunning ? "var(--accent)" : "var(--text-dim)"
    badge.textContent = (isRunning ? "\u27F3 " : "\u25CB ") + (ws.containerStatus || "unknown")
    actions.appendChild(badge)

    if (isRunning) {
      var stopBtn = el("button", "workspace-btn-stop", "Stop")
      stopBtn.addEventListener("click", function () { workspaceAction(ws.name, "stop") })
      actions.appendChild(stopBtn)
    } else if (isStopped || ws.containerStatus === "not_found") {
      var startBtn = el("button", "workspace-btn-start", "Start")
      startBtn.addEventListener("click", function () { workspaceAction(ws.name, "start") })
      actions.appendChild(startBtn)
    }

    top.appendChild(actions)
    card.appendChild(top)

    // Template badge
    if (ws.template) {
      var tmplBadge = el("span", "workspace-card-template", "\u25A3 " + ws.template)
      card.appendChild(tmplBadge)
    }

    // Info rows
    if (ws.image) card.appendChild(el("div", "workspace-card-desc", "Image: " + ws.image))

    // Stats bars (only for running containers)
    if (isRunning && ws.memLimit > 0) {
      var memPercent = Math.round((ws.memUsage / ws.memLimit) * 100)
      var statsRow = el("div", "workspace-card-stats")
      statsRow.appendChild(el("span", "", "CPU: " + (ws.cpuPercent || 0).toFixed(1) + "%"))
      statsRow.appendChild(el("span", "", "Mem: " + formatBytes(ws.memUsage) + " / " + formatBytes(ws.memLimit) + " (" + memPercent + "%)"))
      card.appendChild(statsRow)
    }

    // Path
    if (ws.path) {
      var pathLine = el("div", "workspace-card-path")
      pathLine.textContent = ws.path
      card.appendChild(pathLine)
    }

    // Active tasks
    if (ws.activeTasks > 0) {
      card.appendChild(el("div", "workspace-card-agents", ws.activeTasks + " active task" + (ws.activeTasks !== 1 ? "s" : "")))
    }

    return card
  }

  function workspaceAction(name, action) {
    var headers = authHeaders()
    fetch(apiPathWithToken("/api/workspaces/" + encodeURIComponent(name) + "/" + action), {
      method: "POST",
      headers: headers,
    })
      .then(function () {
        // Refresh workspace view
        if (state.activeView === "workspaces") {
          var contentEl = document.querySelector(".ws-content")
          if (contentEl) fetchWorkspaces(contentEl)
        }
      })
      .catch(function (err) {
        console.error("workspaceAction " + action + ":", err)
      })
  }

  function formatBytes(bytes) {
    if (!bytes || bytes === 0) return "0 B"
    var units = ["B", "KB", "MB", "GB"]
    var i = 0
    var val = bytes
    while (val >= 1024 && i < units.length - 1) { val /= 1024; i++ }
    return val.toFixed(i > 1 ? 1 : 0) + " " + units[i]
  }

  // ── Templates sub-tab ──────────────────────────────────────────────

  function fetchTemplates(container) {
    fetch(apiPathWithToken("/api/templates"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("templates fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        buildTemplatesContent(container, data.templates || [])
      })
      .catch(function () {
        buildTemplatesContent(container, [])
      })
  }

  function buildTemplatesContent(container, templates) {
    clearChildren(container)

    if (!templates || !templates.length) {
      var emptyCard = el("div", "template-card")
      emptyCard.appendChild(el("div", "template-card-name", "No templates available"))
      container.appendChild(emptyCard)
      return
    }

    // Fetch workspace counts per template
    fetch(apiPathWithToken("/api/workspaces"), { headers: authHeaders() })
      .then(function (r) { return r.ok ? r.json() : { workspaces: [] } })
      .then(function (data) {
        var counts = {}
        var ws = data.workspaces || []
        for (var i = 0; i < ws.length; i++) {
          if (ws[i].template) {
            counts[ws[i].template] = (counts[ws[i].template] || 0) + 1
          }
        }
        renderTemplateCards(container, templates, counts)
      })
      .catch(function () {
        renderTemplateCards(container, templates, {})
      })
  }

  function renderTemplateCards(container, templates, usedByCounts) {
    clearChildren(container)
    var grid = el("div", "template-grid")
    for (var i = 0; i < templates.length; i++) {
      grid.appendChild(createTemplateCard(templates[i], usedByCounts[templates[i].name] || 0))
    }
    container.appendChild(grid)
  }

  function createTemplateCard(tmpl, usedByCount) {
    var card = el("div", "template-card")

    // Top row: name + built-in badge
    var top = el("div", "template-card-top")
    top.appendChild(el("span", "template-card-name", tmpl.name))
    if (tmpl.builtIn) {
      top.appendChild(el("span", "template-badge-builtin", "built-in"))
    }
    card.appendChild(top)

    // Description
    if (tmpl.description) {
      card.appendChild(el("div", "template-card-desc", tmpl.description))
    }

    // Image
    card.appendChild(el("div", "template-card-image", "\u25A3 " + tmpl.image))

    // Resources
    var resources = []
    if (tmpl.cpuDefault) resources.push(tmpl.cpuDefault + " CPU")
    if (tmpl.memoryDefault) resources.push(formatBytes(tmpl.memoryDefault) + " RAM")
    if (resources.length) {
      card.appendChild(el("div", "template-card-resources", resources.join(" \u00B7 ")))
    }

    // Tags
    if (tmpl.tags && tmpl.tags.length) {
      var tagsRow = el("div", "template-card-tags")
      for (var i = 0; i < tmpl.tags.length; i++) {
        tagsRow.appendChild(el("span", "template-tag", tmpl.tags[i]))
      }
      card.appendChild(tagsRow)
    }

    // Used by count
    if (usedByCount > 0) {
      card.appendChild(el("div", "template-card-usage", "Used by " + usedByCount + " workspace" + (usedByCount !== 1 ? "s" : "")))
    }

    // Create workspace button
    var createBtn = el("button", "template-create-btn", "Create Workspace")
    createBtn.addEventListener("click", function () {
      openAddProjectModalWithTemplate(tmpl)
    })
    card.appendChild(createBtn)

    return card
  }

  function openAddProjectModalWithTemplate(tmpl) {
    openAddProjectModal()
    applyTemplateToModal(tmpl)
  }

  function applyTemplateToModal(tmpl) {
    // Set container mode to "provision"
    var radios = document.querySelectorAll('input[name="container-mode"]')
    for (var i = 0; i < radios.length; i++) {
      radios[i].checked = radios[i].value === "provision"
    }
    updateContainerFields()

    // Fill fields from template
    if (addProjectImage) addProjectImage.value = tmpl.image || ""
    if (addProjectCpu) addProjectCpu.value = tmpl.cpuDefault || 2
    if (addProjectMem) addProjectMem.value = tmpl.memoryDefault ? (tmpl.memoryDefault / (1024 * 1024 * 1024)).toFixed(1) : "2"

    // Highlight template in picker
    highlightTemplatePicker(tmpl.name)

    // Store template name for submission
    if (addProjectModal) addProjectModal.dataset.template = tmpl.name
  }

  function highlightTemplatePicker(name) {
    var btns = document.querySelectorAll(".template-pick-btn")
    for (var i = 0; i < btns.length; i++) {
      if (btns[i].dataset.template === name) {
        btns[i].classList.add("template-pick-btn--active")
      } else {
        btns[i].classList.remove("template-pick-btn--active")
      }
    }
  }

  function populateTemplatePicker() {
    var pickerEl = document.getElementById("template-picker")
    if (!pickerEl) return

    clearChildren(pickerEl)

    // "Custom" option
    var customBtn = el("button", "template-pick-btn template-pick-btn--active", "Custom")
    customBtn.type = "button"
    customBtn.dataset.template = ""
    customBtn.addEventListener("click", function () {
      highlightTemplatePicker("")
      if (addProjectModal) addProjectModal.dataset.template = ""
    })
    pickerEl.appendChild(customBtn)

    fetch(apiPathWithToken("/api/templates"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("templates fetch failed")
        return r.json()
      })
      .then(function (data) {
        var templates = data.templates || []
        for (var i = 0; i < templates.length; i++) {
          (function (tmpl) {
            var btn = el("button", "template-pick-btn", tmpl.name)
            btn.type = "button"
            btn.dataset.template = tmpl.name
            btn.addEventListener("click", function () {
              applyTemplateToModal(tmpl)
            })
            pickerEl.appendChild(btn)
          })(templates[i])
        }
      })
      .catch(function () {
        // API not available — picker stays with just "Custom"
      })
  }

  // ── Filter bar ────────────────────────────────────────────────────
  function renderFilterBar() {
    var filterBar = document.getElementById("filter-bar")
    if (!filterBar) return

    clearChildren(filterBar)

    // "All" pill with task count
    var allLabel = "All (" + state.tasks.length + ")"
    var allPill = el("button", "filter-pill" + (state.projectFilter === "" ? " filter-pill--active" : ""), allLabel)
    allPill.dataset.project = ""
    allPill.addEventListener("click", handleFilterClick)
    filterBar.appendChild(allPill)

    // Project pills
    for (var i = 0; i < state.projects.length; i++) {
      var name = state.projects[i].name
      var active = state.projectFilter === name
      var pill = el("button", "filter-pill" + (active ? " filter-pill--active" : ""), name)
      pill.dataset.project = name
      pill.addEventListener("click", handleFilterClick)
      filterBar.appendChild(pill)
    }

    // Re-add the "+ Project" button (cleared by clearChildren above)
    var addBtn = el("button", "filter-btn", "+ Project")
    addBtn.id = "add-project-btn"
    addBtn.title = "Add Project"
    addBtn.addEventListener("click", openAddProjectModal)
    filterBar.appendChild(addBtn)
  }

  function handleFilterClick(e) {
    state.projectFilter = e.currentTarget.dataset.project || ""
    state.chatModeOverride = null
    renderFilterBar()
    renderTaskList()
    renderChatBar()
  }

  // ── Tier definitions ──────────────────────────────────────────────
  var TIER_DEFS = [
    { key: "needsAttention", label: "Needs Attention", cssVar: "--needsAttention" },
    { key: "active",         label: "Active",          cssVar: "--active" },
    { key: "recent",         label: "Recent",          cssVar: "--recent" },
    { key: "idle",           label: "Idle",            cssVar: "--idle" },
  ]

  var RECENT_THRESHOLD_MS = 30 * 60 * 1000

  function assignTaskTier(task) {
    // Prefer server-provided tier if present
    if (task.tier) return
    var s = effectiveAgentStatus(task)
    if (s === "waiting" || s === "error") {
      task.tier = "needsAttention"
      task.tierBadge = s === "waiting" ? "approval" : "error"
    } else if (s === "running" || s === "thinking" || s === "starting") {
      task.tier = "active"
      task.tierBadge = ""
    } else if (s === "idle" || s === "") {
      var updatedAt = task.updatedAt ? new Date(task.updatedAt).getTime() : 0
      if (updatedAt && (Date.now() - updatedAt) < RECENT_THRESHOLD_MS) {
        task.tier = "recent"
      } else {
        task.tier = "idle"
      }
      task.tierBadge = ""
    } else if (s === "complete") {
      task.tier = "idle"
      task.tierBadge = ""
    } else {
      task.tier = "idle"
      task.tierBadge = ""
    }
  }

  // Track collapsed state for tier sections
  var tierCollapsed = { idle: true }

  // ── Task list ─────────────────────────────────────────────────────
  function renderTaskList() {
    var taskList = document.getElementById("task-list")
    var emptyEl = document.getElementById("task-list-empty")
    if (!taskList) return

    // Filter tasks
    var visible = state.tasks.filter(function (t) {
      if (state.projectFilter && t.project !== state.projectFilter) return false
      return true
    })

    // Remove existing tier sections
    var existing = taskList.querySelectorAll(".tier-section")
    for (var i = 0; i < existing.length; i++) {
      existing[i].remove()
    }
    // Also remove any legacy cards/headers outside tier sections
    var legacy = taskList.querySelectorAll(".agent-card, .task-section-header")
    for (var li = 0; li < legacy.length; li++) {
      legacy[li].remove()
    }

    if (visible.length === 0) {
      if (emptyEl) {
        emptyEl.style.display = ""
        emptyEl.textContent = state.tasks.length === 0
          ? "No agents yet."
          : "No agents match the current filter."
      }
      return
    }

    if (emptyEl) emptyEl.style.display = "none"

    // Assign tiers to each task
    for (var j = 0; j < visible.length; j++) {
      assignTaskTier(visible[j])
    }

    // Group by tier
    var tierBuckets = {}
    for (var td = 0; td < TIER_DEFS.length; td++) {
      tierBuckets[TIER_DEFS[td].key] = []
    }
    for (var k = 0; k < visible.length; k++) {
      var tierKey = visible[k].tier || "idle"
      if (!tierBuckets[tierKey]) tierBuckets[tierKey] = []
      tierBuckets[tierKey].push(visible[k])
    }

    // Render each non-empty tier section
    for (var t = 0; t < TIER_DEFS.length; t++) {
      var def = TIER_DEFS[t]
      var bucket = tierBuckets[def.key]
      if (bucket.length === 0) continue

      var section = el("div", "tier-section tier-section" + def.cssVar)
      section.dataset.tier = def.key

      // Header
      var header = el("div", "tier-header tier-header" + def.cssVar)
      var headerLeft = el("span", null)

      // Pulse dot for active tier
      if (def.key === "active") {
        var dot = el("span", "pulse-dot")
        headerLeft.appendChild(dot)
        headerLeft.appendChild(document.createTextNode(" "))
      }

      headerLeft.appendChild(document.createTextNode(def.label))
      header.appendChild(headerLeft)

      var badge = el("span", "tier-badge", bucket.length.toString())
      header.appendChild(badge)

      var isCollapsed = !!tierCollapsed[def.key]
      if (isCollapsed) {
        section.classList.add("tier-collapsed")
      }

      // Toggle collapse on header click (needsAttention always expanded)
      ;(function (sectionEl, tierKey) {
        header.addEventListener("click", function () {
          if (tierKey === "needsAttention") return
          tierCollapsed[tierKey] = !tierCollapsed[tierKey]
          sectionEl.classList.toggle("tier-collapsed")
        })
      })(section, def.key)

      header.style.cursor = "pointer"
      section.appendChild(header)

      // Cards
      for (var c = 0; c < bucket.length; c++) {
        section.appendChild(createAgentCard(bucket[c]))
      }

      taskList.appendChild(section)
    }
  }

  // ── Agent card ────────────────────────────────────────────────────
  function createAgentCard(task) {
    var isSelected = state.selectedTaskId === task.id
    var card = el("div", "agent-card" + (isSelected ? " agent-card--selected" : ""))
    card.dataset.taskId = task.id
    card.setAttribute("role", "button")
    card.setAttribute("tabindex", "0")

    // Left border color: orange for waiting agents, otherwise from task status
    var borderColor = getCardBorderColor(task)
    card.style.borderLeftColor = borderColor

    // Top row: project name (left) + INPUT badge + agent status (right)
    var top = el("div", "agent-card-top")
    top.appendChild(el("span", "agent-card-project", task.project || "\u2014"))
    var topRight = el("div", "agent-card-footer")
    topRight.style.gap = "6px"
    var cardStatus = effectiveAgentStatus(task)
    if (cardStatus === "waiting" && task.askQuestion) {
      topRight.appendChild(el("span", "ask-badge", "\u25D0 INPUT"))
    }
    topRight.appendChild(createAgentStatusBadge(cardStatus))
    top.appendChild(topRight)
    card.appendChild(top)

    // Description
    if (task.description) {
      card.appendChild(el("div", "agent-card-desc", task.description))
    }

    // Footer: task ID + time (left) | mini session chain bars (right)
    var footer = el("div", "agent-card-footer")
    footer.style.justifyContent = "space-between"
    var idTime = el("span", "agent-card-time", task.id + " \u00B7 " + (task.time || formatDuration(task.createdAt)))
    footer.appendChild(idTime)
    footer.appendChild(createMiniSessionChain(task))
    card.appendChild(footer)

    // Click handler
    card.addEventListener("click", function () {
      selectTask(task.id)
    })
    card.addEventListener("keydown", function (e) {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault()
        selectTask(task.id)
      }
    })

    return card
  }

  // ── Agent status badge ────────────────────────────────────────────
  function createAgentStatusBadge(agentStatus) {
    var meta = AGENT_STATUS_META[agentStatus] || AGENT_STATUS_META.idle
    var badge = el("span", "agent-status-badge")
    var icon = el("span", "agent-status-badge-icon", meta.icon)
    icon.style.color = meta.color
    badge.appendChild(icon)
    badge.appendChild(document.createTextNode(" " + meta.label))
    return badge
  }

  // ── Mini session chain (colored bars in card footer) ──────────────
  function createMiniSessionChain(task) {
    var chain = el("div", "mini-session-chain")

    // Only show mini bars when task has actual sessions
    var segments = []
    if (task.sessions && task.sessions.length > 0) {
      for (var i = 0; i < task.sessions.length; i++) {
        segments.push({
          phase: task.sessions[i].phase,
          status: task.sessions[i].status,
        })
      }
    }

    for (var k = 0; k < segments.length; k++) {
      var bar = el("div", "mini-bar")
      if (segments[k].status === "complete") {
        bar.style.background = "var(--green)"
      } else if (segments[k].status === "active") {
        bar.style.background = PHASE_COLORS_HEX[segments[k].phase] || "var(--accent)"
      }
      bar.title = segments[k].phase
      chain.appendChild(bar)
    }

    return chain
  }

  // ── Task selection ────────────────────────────────────────────────
  function selectTask(id) {
    state.selectedTaskId = id
    state.chatModeOverride = null
    var task = findTask(id)

    // Update card selection styles
    var cards = document.querySelectorAll(".agent-card")
    for (var i = 0; i < cards.length; i++) {
      if (cards[i].dataset.taskId === id) {
        cards[i].classList.add("agent-card--selected")
        if (task) {
          cards[i].style.borderLeftColor = getCardBorderColor(task)
        }
      } else {
        cards[i].classList.remove("agent-card--selected")
        var otherTask = findTask(cards[i].dataset.taskId)
        if (otherTask) {
          cards[i].style.borderLeftColor = getCardBorderColor(otherTask)
        }
      }
    }

    if (task) {
      renderRightPanel(task)
      connectTerminal(task)
    }

    renderTopBar()

    // Mobile: show detail panel
    var panels = document.getElementById("panels")
    if (panels) panels.classList.add("detail-active")

    renderChatBar()
    renderAskBanner()
  }

  // ── Right panel ───────────────────────────────────────────────────
  function renderRightPanel(task) {
    var emptyState = document.getElementById("empty-state")
    var detailView = document.getElementById("detail-view")

    if (!task) {
      if (emptyState) emptyState.style.display = ""
      if (detailView) detailView.style.display = "none"
      return
    }

    if (emptyState) emptyState.style.display = "none"
    if (detailView) detailView.style.display = ""

    renderDetailHeader(task)
    renderSessionChain(task)
    renderPreviewHeader(task)
    renderClaudeMeta(task)

    // If the Messages tab is currently active, reload messages for the new task.
    var activeTab = document.querySelector(".detail-tab--active")
    if (activeTab && activeTab.dataset.tab === "messages") {
      var msgSessionId = getSessionIdForMessages(task)
      if (msgSessionId) {
        if (msgSessionId !== state.lastLoadedMessagesSession) {
          loadSessionMessages(msgSessionId)
        }
      } else {
        // New task has no session — clear stale messages from previous task.
        state.lastLoadedMessagesSession = null
        var mc = document.getElementById("messages-container")
        if (mc) {
          clearChildren(mc)
          mc.appendChild(el("div", "terminal-placeholder", "No conversation data available."))
        }
      }
    }
  }

  // ── Detail header ─────────────────────────────────────────────────
  function renderDetailHeader(task) {
    var header = document.getElementById("detail-header")
    if (!header) return

    clearChildren(header)

    // Top row: back button + title + actions
    var top = el("div", "detail-header-top")

    var backBtn = el("button", "detail-back-btn", "\u2190 Back")
    backBtn.addEventListener("click", handleMobileBack)
    top.appendChild(backBtn)

    top.appendChild(el("span", "detail-title", task.description || "\u2014"))

    var actions = el("div", "detail-actions")
    actions.appendChild(createAgentStatusBadge(effectiveAgentStatus(task)))
    top.appendChild(actions)

    header.appendChild(top)

    // Meta row: project / id + branch + tmux session + skills
    var meta = el("div", "detail-meta")
    meta.appendChild(el("span", null, (task.project || "\u2014") + " / " + task.id))
    if (task.branch) {
      meta.appendChild(el("span", null, "\u2192 " + task.branch))
    }
    if (task.tmuxSession) {
      var tmuxTag = el("span", null, "tmux: " + task.tmuxSession)
      tmuxTag.style.cssText = "color: var(--text-dim); background: var(--bg-panel); padding: 1px 6px; border-radius: 2px; font-size: 0.5rem;"
      meta.appendChild(tmuxTag)
    }
    var skills = task.skills || []
    for (var sk = 0; sk < skills.length; sk++) {
      var skillTag = el("span", null, skills[sk])
      skillTag.style.cssText = "color: var(--purple); background: rgba(139,140,248,0.08); padding: 1px 6px; border-radius: 2px; font-size: 0.56rem;"
      meta.appendChild(skillTag)
    }
    header.appendChild(meta)
  }

  // ── Session chain (detail panel) ──────────────────────────────────
  function renderSessionChain(task) {
    var container = document.getElementById("session-chain")
    if (!container) return

    clearChildren(container)

    // Use sessions array if available, otherwise fall back to phase pips
    var phases = []
    if (task.sessions && task.sessions.length > 0) {
      for (var i = 0; i < task.sessions.length; i++) {
        var s = task.sessions[i]
        phases.push({
          label: s.phase,
          dotLabel: (s.phase || "?").charAt(0).toUpperCase(),
          status: s.status === "complete" ? "done" : (s.status === "active" ? "active" : ""),
          duration: s.duration || "",
          artifact: s.artifact || "",
        })
      }
    } else if (task.phase) {
      var currentIdx = PHASES.indexOf(task.phase)
      for (var j = 0; j < PHASES.length; j++) {
        var st = ""
        if (j < currentIdx) st = "done"
        else if (j === currentIdx) st = "active"
        phases.push({
          label: PHASES[j],
          dotLabel: PHASE_DOT_LABELS[PHASES[j]],
          status: st,
          duration: "",
          artifact: "",
        })
      }
    }

    for (var k = 0; k < phases.length; k++) {
      var phaseColor = PHASE_COLORS_HEX[phases[k].label] || "#4a5368"

      // Connector
      if (k > 0) {
        var conn = el("div", "session-chain-connector")
        if (phases[k - 1].status === "done") {
          conn.style.background = "rgba(45, 212, 160, 0.38)"
        }
        container.appendChild(conn)
      }

      // Pip
      var pip = el("div", "session-chain-pip")

      var dot = el("div", "session-chain-dot")
      dot.style.borderColor = phaseColor
      if (phases[k].status === "done") {
        dot.style.background = phaseColor
        dot.style.color = "var(--bg)"
      } else if (phases[k].status === "active") {
        dot.style.background = "transparent"
        dot.style.color = phaseColor
        dot.style.boxShadow = "0 0 6px " + phaseColor + "60"
      } else {
        dot.style.background = "transparent"
        dot.style.color = "var(--text-dim)"
      }
      pip.appendChild(dot)

      var lbl = el("div", "session-chain-label")
      lbl.style.color = phases[k].status === "active" ? phaseColor : "var(--text-dim)"
      if (phases[k].status === "active") lbl.style.fontWeight = "600"
      lbl.textContent = phases[k].label
      pip.appendChild(lbl)

      if (phases[k].duration) {
        var dur = el("div", "session-chain-label")
        dur.style.fontSize = "0.5rem"
        dur.textContent = phases[k].duration
        pip.appendChild(dur)
      }

      container.appendChild(pip)
    }

    // Phase transition button
    var currentPhaseIdx = PHASES.indexOf(task.phase)
    if (task.status !== "done" && currentPhaseIdx >= 0 && currentPhaseIdx < PHASES.length - 1) {
      var nextPhase = PHASES[currentPhaseIdx + 1]
      var transBtn = el("button", "phase-transition-btn", "\u2192 " + phaseLabel(nextPhase))
      transBtn.dataset.taskId = task.id
      transBtn.dataset.nextPhase = nextPhase
      transBtn.addEventListener("click", handlePhaseTransition)
      container.appendChild(transBtn)
    }
  }

  function phaseLabel(phase) {
    return phase.charAt(0).toUpperCase() + phase.slice(1)
  }

  // ── Preview header (rich) ──────────────────────────────────────────
  function renderPreviewHeader(task) {
    var container = document.getElementById("preview-header")
    if (!container) return

    clearChildren(container)

    // "PREVIEW" label
    container.appendChild(el("div", "preview-header-label", "Preview"))

    // Row: project + agent status | NEEDS INPUT badge
    var row = el("div", "preview-header-row")

    var leftGroup = el("div", null)
    leftGroup.style.display = "flex"
    leftGroup.style.alignItems = "center"
    leftGroup.appendChild(el("span", "preview-header-project", task.project || "\u2014"))

    var effectiveStatus = effectiveAgentStatus(task)
    var agentMeta = AGENT_STATUS_META[effectiveStatus] || AGENT_STATUS_META.idle
    var statusSpan = el("span", "preview-header-agent-status")
    statusSpan.textContent = agentMeta.icon + " " + agentMeta.label
    statusSpan.style.color = agentMeta.color
    if (agentMeta === AGENT_STATUS_META.waiting || agentMeta === AGENT_STATUS_META.thinking) {
      statusSpan.style.animation = "pulse-dot 1.5s ease-in-out infinite"
    }
    leftGroup.appendChild(statusSpan)
    row.appendChild(leftGroup)

    // NEEDS INPUT badge
    if (effectiveStatus === "waiting" && task.askQuestion) {
      var needsInput = el("span", "preview-header-needs-input", "NEEDS INPUT")
      needsInput.style.background = "rgba(245, 158, 11, 0.12)"
      needsInput.style.border = "1px solid rgba(245, 158, 11, 0.25)"
      needsInput.style.color = "var(--orange)"
      row.appendChild(needsInput)
    }
    container.appendChild(row)

    // Meta: workspace path + active time
    var meta = el("div", "preview-header-meta")
    var path = task.workspacePath || "/workspace/" + (task.project || "unknown")
    meta.appendChild(document.createTextNode("\uD83D\uDCC1 " + path))
    if (task.createdAt || task.time) {
      var timeSpan = el("span", "preview-header-meta-time")
      timeSpan.textContent = "\u23F1 active " + (task.time || formatDuration(task.createdAt))
      meta.appendChild(timeSpan)
    }
    container.appendChild(meta)

    // Skills badges
    var skills = task.skills || []
    if (skills.length > 0) {
      var skillsRow = el("div", "preview-header-skills")
      for (var i = 0; i < skills.length; i++) {
        skillsRow.appendChild(el("span", "preview-header-skill", skills[i]))
      }
      container.appendChild(skillsRow)
    }
  }

  // ── Claude metadata section ──────────────────────────────────────
  function renderClaudeMeta(task) {
    var container = document.getElementById("claude-meta")
    if (!container) return

    clearChildren(container)

    // Row 1: connection status + session ID
    var row1 = el("div", "claude-meta-row")

    var statusLabel = el("span", null)
    statusLabel.appendChild(document.createTextNode("Status: "))
    var liveSession = getActiveSessionForTask(task)
    if (liveSession && liveSession.status !== "error") {
      var connSpan = el("span", "claude-meta-connected", "\u25CF Connected")
      statusLabel.appendChild(connSpan)
    } else if (liveSession && liveSession.status === "error") {
      var errSpan = el("span", "claude-meta-disconnected", "\u25CB Session exited")
      statusLabel.appendChild(errSpan)
    } else if (task.tmuxSession) {
      var connSpan2 = el("span", "claude-meta-connected", "\u25CF Connected")
      statusLabel.appendChild(connSpan2)
    } else {
      var discSpan = el("span", "claude-meta-disconnected", "\u25CB No session")
      statusLabel.appendChild(discSpan)
    }
    row1.appendChild(statusLabel)

    // Session ID (from active session in chain)
    var activeSession = null
    if (task.sessions) {
      for (var i = 0; i < task.sessions.length; i++) {
        if (task.sessions[i].status === "active") {
          activeSession = task.sessions[i]
          break
        }
      }
    }
    if (activeSession && activeSession.claudeSessionId) {
      var sidLabel = el("span", null)
      sidLabel.appendChild(document.createTextNode("Session: "))
      sidLabel.appendChild(el("span", "claude-meta-session-id", activeSession.claudeSessionId))
      row1.appendChild(sidLabel)
    }
    container.appendChild(row1)

    // MCPs
    var mcps = task.mcps || []
    if (mcps.length > 0) {
      var mcpRow = el("div", "claude-meta-mcps")
      mcpRow.appendChild(document.createTextNode("MCPs: "))
      for (var m = 0; m < mcps.length; m++) {
        mcpRow.appendChild(el("span", "claude-meta-mcp-name", mcps[m] + " \u00D7"))
      }
      container.appendChild(mcpRow)
    }

    // Fork hints
    var hints = el("div", "claude-meta-hints")
    hints.appendChild(document.createTextNode("Fork: "))
    var keyHint = el("span", "claude-meta-hint-key", "f quick fork, F fork with options")
    hints.appendChild(keyHint)
    container.appendChild(hints)
  }

  // ── OutputBuffer (throttles writes to xterm.js) ──────────────────
  function OutputBuffer(terminal) {
    this.terminal = terminal
    this.buffer = ""
    this.pending = false
    this.maxBufferSize = 65536 // 64 KB
  }

  OutputBuffer.prototype.write = function (data) {
    this.buffer += data
    if (this.buffer.length >= this.maxBufferSize) {
      this.flush()
      return
    }
    if (!this.pending) {
      this.pending = true
      var self = this
      requestAnimationFrame(function () {
        self.flush()
      })
    }
  }

  OutputBuffer.prototype.flush = function () {
    if (this.buffer.length > 0) {
      this.terminal.write(this.buffer)
      this.buffer = ""
    }
    this.pending = false
  }

  // ── Tool Renderers ──────────────────────────────────────────────
  var ToolRenderers = {
    _renderers: Object.create(null),

    register: function (name, renderer) {
      this._renderers[name] = renderer
    },

    get: function (name) {
      return this._renderers[name] || this._defaultRenderer
    },

    render: function (name, input, result, augment) {
      var renderer = this.get(name)
      return renderer(input, result, augment)
    },

    _defaultRenderer: function (input, result) {
      var block = el("div", "tool-block")
      var header = el("div", "tool-header")
      var icon = el("span", "tool-icon", "\u2699")
      header.appendChild(icon)
      var label = el("span", "tool-command", "Tool Result")
      header.appendChild(label)
      block.appendChild(header)

      var body = el("div", "tool-body")
      if (input) {
        var inputPre = document.createElement("pre")
        inputPre.appendChild(document.createTextNode(JSON.stringify(input, null, 2)))
        body.appendChild(inputPre)
      }
      if (result) {
        var resultPre = document.createElement("pre")
        resultPre.appendChild(document.createTextNode(JSON.stringify(result, null, 2)))
        body.appendChild(resultPre)
      }
      block.appendChild(body)
      return block
    },
  }

  function escapeHtml(s) {
    if (!s) return ""
    var div = document.createElement("div")
    div.textContent = s
    return div.innerHTML
  }

  // ── Bash Renderer ──────────────────────────────────────────────
  ToolRenderers.register("Bash", function (input, result, augment) {
    var block = el("div", "tool-block")
    var header = el("div", "tool-header")

    var icon = el("span", "tool-icon", "$")
    header.appendChild(icon)

    var command = input && input.command ? input.command : ""
    var cmdSpan = el("span", "tool-command")
    cmdSpan.textContent = command
    header.appendChild(cmdSpan)

    // Error badge if exit code != 0
    var exitCode = result && result.exitCode != null ? result.exitCode : 0
    if (exitCode !== 0) {
      var badge = el("span", "tool-badge tool-badge--error", "exit " + exitCode)
      header.appendChild(badge)
    }

    header.style.cursor = "pointer"
    block.appendChild(header)

    var body = el("div", "tool-body tool-collapsed")

    // stdout
    var stdout = result && result.stdout ? result.stdout : (result && typeof result === "string" ? result : "")
    if (stdout) {
      var stdoutPre = document.createElement("pre")
      stdoutPre.appendChild(document.createTextNode(stdout))
      body.appendChild(stdoutPre)
    }

    // stderr
    var stderr = result && result.stderr ? result.stderr : ""
    if (stderr) {
      var stderrPre = document.createElement("pre")
      stderrPre.className = "tool-stderr"
      stderrPre.appendChild(document.createTextNode(stderr))
      body.appendChild(stderrPre)
    }

    block.appendChild(body)

    header.addEventListener("click", function () {
      body.classList.toggle("tool-collapsed")
    })

    return block
  })

  // ── Edit Renderer ──────────────────────────────────────────────
  ToolRenderers.register("Edit", function (input, result, augment) {
    var block = el("div", "tool-block")
    var header = el("div", "tool-header")

    var icon = el("span", "tool-icon", "\u270E")
    header.appendChild(icon)

    var filename = input && input.file_path ? input.file_path : (input && input.filePath ? input.filePath : "unknown")
    var fnSpan = el("span", "tool-filename")
    fnSpan.textContent = filename
    header.appendChild(fnSpan)

    // +N / -N badges from augment
    if (augment) {
      if (augment.additions != null && augment.additions > 0) {
        var addBadge = el("span", "tool-badge tool-badge--add", "+" + augment.additions)
        header.appendChild(addBadge)
      }
      if (augment.deletions != null && augment.deletions > 0) {
        var delBadge = el("span", "tool-badge tool-badge--del", "-" + augment.deletions)
        header.appendChild(delBadge)
      }
    }

    header.style.cursor = "pointer"
    block.appendChild(header)

    var body = el("div", "tool-body tool-collapsed")

    // Server-rendered diff HTML (pre-sanitized by Go server's escapeHTML)
    if (augment && augment.diffHtml) {
      var diffContainer = document.createElement("div")
      diffContainer.setAttribute("data-server-rendered", "true")
      // Safe: diffHtml is pre-sanitized by server-side escapeHTML()
      diffContainer.insertAdjacentHTML("beforeend", augment.diffHtml)
      body.appendChild(diffContainer)
    } else if (result) {
      var resultPre = document.createElement("pre")
      resultPre.appendChild(document.createTextNode(typeof result === "string" ? result : JSON.stringify(result, null, 2)))
      body.appendChild(resultPre)
    }

    block.appendChild(body)

    header.addEventListener("click", function () {
      body.classList.toggle("tool-collapsed")
    })

    return block
  })

  // ── Read Renderer ──────────────────────────────────────────────
  ToolRenderers.register("Read", function (input, result, augment) {
    var block = el("div", "tool-block")
    var header = el("div", "tool-header")

    var icon = el("span", "tool-icon", "\uD83D\uDCC4")
    header.appendChild(icon)

    var filename = input && input.file_path ? input.file_path : (input && input.filePath ? input.filePath : "unknown")
    var fnSpan = el("span", "tool-filename")
    fnSpan.textContent = filename
    header.appendChild(fnSpan)

    // Line count badge
    var content = result && typeof result === "string" ? result : ""
    if (content) {
      var lines = content.split("\n").length
      var linesBadge = el("span", "tool-badge", lines + " lines")
      header.appendChild(linesBadge)
    }

    header.style.cursor = "pointer"
    block.appendChild(header)

    var body = el("div", "tool-body tool-collapsed")

    // Server-rendered highlighted content from augment
    if (augment && augment.highlightedHtml) {
      var hlContainer = document.createElement("div")
      hlContainer.setAttribute("data-server-rendered", "true")
      // Safe: highlightedHtml is pre-sanitized by server-side escapeHTML()
      hlContainer.insertAdjacentHTML("beforeend", augment.highlightedHtml)
      body.appendChild(hlContainer)
    } else if (content) {
      var contentPre = document.createElement("pre")
      contentPre.appendChild(document.createTextNode(content))
      body.appendChild(contentPre)
    }

    block.appendChild(body)

    header.addEventListener("click", function () {
      body.classList.toggle("tool-collapsed")
    })

    return block
  })

  // ── Terminal management ───────────────────────────────────────────
  function connectTerminal(task) {
    disconnectTerminal()
    var container = document.getElementById("terminal-container")
    if (!container) return
    clearChildren(container)

    // Check if Terminal (xterm.js) is available
    if (typeof Terminal === "undefined") {
      var fallback = el("div", "terminal-placeholder", "Terminal emulator not available. Check xterm.js assets.")
      container.appendChild(fallback)
      return
    }

    // Resolve session identity for the WebSocket connection:
    // The WS handler matches by session.Instance ID, not tmux session name.
    // 1. Try live session from session map (real session.Instance)
    // 2. Container sessions use SSE preview (no local session.Instance)
    var liveSession = getActiveSessionForTask(task)
    var isContainerTask = !liveSession && task.tmuxSession

    var toolbar = document.getElementById("terminal-toolbar")

    if (!liveSession && !isContainerTask) {
      var placeholder = el("div", "terminal-placeholder", "No session attached.")
      container.appendChild(placeholder)
      if (toolbar) toolbar.style.display = "none"
      return
    }

    if (toolbar) toolbar.style.display = ""

    var termFontSize = state.terminalFontSize || 14
    var term = new Terminal({
      cursorBlink: true,
      cursorStyle: "bar",
      cursorWidth: 2,
      fontSize: termFontSize,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'IBM Plex Mono', monospace",
      fontWeight: "400",
      fontWeightBold: "600",
      lineHeight: 1.2,
      letterSpacing: 0,
      scrollback: 10000,
      allowProposedApi: true,
      theme: {
        background: "#080a0e",
        foreground: "#c8d0dc",
        cursor: "#e8a932",
        cursorAccent: "#080a0e",
        selectionBackground: "rgba(232, 169, 50, 0.25)",
        selectionForeground: "#ffffff",
        black: "#1a1e2a",
        red: "#f06060",
        green: "#2dd4a0",
        yellow: "#e8a932",
        blue: "#4ca8e8",
        magenta: "#c084fc",
        cyan: "#22d3ee",
        white: "#c8d0dc",
        brightBlack: "#4a5368",
        brightRed: "#ff7b7b",
        brightGreen: "#5eead4",
        brightYellow: "#fbbf24",
        brightBlue: "#7cc4f0",
        brightMagenta: "#d4a5ff",
        brightCyan: "#67e8f9",
        brightWhite: "#f0f4fc",
      },
    })
    var fitAddon = new FitAddon.FitAddon()
    term.loadAddon(fitAddon)

    // Web links: make URLs clickable
    if (typeof WebLinksAddon !== "undefined") {
      term.loadAddon(new WebLinksAddon.WebLinksAddon())
    }

    term.open(container)

    // WebGL renderer: smoother rendering (falls back to canvas if unavailable)
    if (typeof WebglAddon !== "undefined") {
      try { term.loadAddon(new WebglAddon.WebglAddon()) } catch (e) { /* canvas fallback */ }
    }

    fitAddon.fit()

    state.terminal = term
    state.fitAddon = fitAddon
    updateTerminalToolbar()

    // Suppress right-click from reaching tmux so only the browser context menu
    // appears. Capture-phase intercept runs before xterm.js can forward it.
    container.addEventListener("mousedown", function (e) {
      if (e.button === 2) e.stopImmediatePropagation()
    }, true)

    // Auto-copy selection to clipboard. Shift+drag bypasses tmux mouse capture
    // to create native xterm.js selections that trigger this handler.
    term.onSelectionChange(function () {
      var sel = term.getSelection()
      if (sel) {
        navigator.clipboard.writeText(sel).catch(function () {})
      }
    })

    // Watch the container for size changes so xterm re-fits automatically.
    if (state.terminalResizeObserver) {
      state.terminalResizeObserver.disconnect()
    }
    state.terminalResizeObserver = new ResizeObserver(function () {
      if (state.fitAddon) {
        state.fitAddon.fit()
        sendTerminalResize()
        updateTerminalToolbar()
      }
    })
    state.terminalResizeObserver.observe(container)

    if (isContainerTask) {
      // Container sessions: stream output via SSE preview endpoint
      connectPreviewStream(task, term)
    } else {
      // Local sessions: connect via WebSocket for live PTY
      connectWebSocket(liveSession.id, term)
    }
  }

  // Send current terminal dimensions to the server so the PTY matches.
  function sendTerminalResize() {
    if (!state.terminal || !state.terminalWs) return
    if (state.terminalWs.readyState !== WebSocket.OPEN) return
    var cols = state.terminal.cols
    var rows = state.terminal.rows
    if (cols > 0 && rows > 0) {
      state.terminalWs.send(JSON.stringify({ type: "resize", cols: cols, rows: rows }))
    }
  }

  // Connect terminal to WebSocket for local sessions with live PTY streaming.
  function connectWebSocket(sessionId, term) {
    var protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
    var wsUrl = protocol + "//" + window.location.host + "/ws/session/" + encodeURIComponent(sessionId)
    if (state.authToken) wsUrl += "?token=" + encodeURIComponent(state.authToken)
    var ws = new WebSocket(wsUrl)
    state.terminalWs = ws

    ws.binaryType = "arraybuffer"
    ws.onopen = function () {
      // Send initial resize so the server PTY matches the browser terminal size.
      sendTerminalResize()
    }
    ws.onmessage = function (e) {
      if (e.data instanceof ArrayBuffer) {
        // Binary frames are PTY output — write directly to terminal.
        // Mouse tracking stays enabled so scroll wheel goes to tmux (full
        // session history). Use Shift+drag for native text selection.
        term.write(new Uint8Array(e.data))
      }
      // String frames are JSON control messages (status, error, etc.) — ignore for terminal
    }
    ws.onclose = function () { state.terminalWs = null }
    term.onData(function (data) {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "input", data: data }))
    })
  }

  // Connect terminal to SSE preview stream for container-based sessions.
  function connectPreviewStream(task, term) {
    var previewUrl = apiPathWithToken("/api/tasks/" + encodeURIComponent(task.id) + "/preview")
    var es = new EventSource(previewUrl)
    state.previewStream = es

    es.addEventListener("preview", function (e) {
      try {
        var data = JSON.parse(e.data)
        if (data.output) {
          term.reset()
          term.write(data.output)
        }
      } catch (err) { /* ignore parse errors */ }
    })
    es.onerror = function () {
      // SSE auto-reconnects; nothing to do here
    }
  }

  function disconnectTerminal() {
    if (state.terminalFullscreen) toggleTerminalFullscreen()
    if (state.terminalResizeObserver) {
      state.terminalResizeObserver.disconnect()
      state.terminalResizeObserver = null
    }
    if (state.terminalWs) {
      state.terminalWs.close()
      state.terminalWs = null
    }
    if (state.previewStream) {
      state.previewStream.close()
      state.previewStream = null
    }
    if (state.terminal) {
      state.terminal.dispose()
      state.terminal = null
    }
    state.fitAddon = null
    var toolbar = document.getElementById("terminal-toolbar")
    if (toolbar) toolbar.style.display = "none"
  }

  // ── Terminal toolbar ──────────────────────────────────────────────
  function updateTerminalToolbar() {
    var dimsEl = document.getElementById("terminal-toolbar-dims")
    var fontEl = document.getElementById("term-font-size")
    if (state.terminal && dimsEl) {
      dimsEl.textContent = state.terminal.cols + "\u00D7" + state.terminal.rows
    }
    if (fontEl) {
      fontEl.textContent = state.terminalFontSize + "px"
    }
  }

  function changeTerminalFontSize(delta) {
    var newSize = Math.max(10, Math.min(24, state.terminalFontSize + delta))
    if (newSize === state.terminalFontSize) return
    state.terminalFontSize = newSize
    if (state.terminal) {
      state.terminal.options.fontSize = newSize
      if (state.fitAddon) {
        state.fitAddon.fit()
        sendTerminalResize()
      }
    }
    updateTerminalToolbar()
  }

  function toggleTerminalFullscreen() {
    var detailView = document.getElementById("detail-view")
    var btn = document.getElementById("term-fullscreen")
    if (!detailView) return
    state.terminalFullscreen = !state.terminalFullscreen
    if (state.terminalFullscreen) {
      detailView.classList.add("terminal-fullscreen")
      if (btn) btn.textContent = "\u2716 Close"
    } else {
      detailView.classList.remove("terminal-fullscreen")
      if (btn) btn.textContent = "\u26F6 Expand"
    }
    if (state.fitAddon) {
      setTimeout(function () {
        state.fitAddon.fit()
        sendTerminalResize()
        updateTerminalToolbar()
      }, 50)
    }
  }

  // Toolbar button event listeners
  ;(function initTerminalToolbar() {
    var fontDown = document.getElementById("term-font-down")
    var fontUp = document.getElementById("term-font-up")
    var scrollBtn = document.getElementById("term-scroll-bottom")
    var fullscreenBtn = document.getElementById("term-fullscreen")

    if (fontDown) fontDown.addEventListener("click", function () { changeTerminalFontSize(-1) })
    if (fontUp) fontUp.addEventListener("click", function () { changeTerminalFontSize(1) })
    if (scrollBtn) scrollBtn.addEventListener("click", function () {
      // Ask server to exit tmux copy-mode (only sends 'q' if pane is in
      // copy-mode, preventing stray keystrokes in the application).
      if (state.terminalWs && state.terminalWs.readyState === WebSocket.OPEN) {
        state.terminalWs.send(JSON.stringify({ type: "scroll_bottom" }))
      }
      if (state.terminal) state.terminal.scrollToBottom()
    })
    if (fullscreenBtn) fullscreenBtn.addEventListener("click", toggleTerminalFullscreen)
  })()

  // ── Phase transition ────────────────────────────────────────────────
  function handlePhaseTransition(e) {
    var taskId = e.currentTarget.dataset.taskId
    var nextPhase = e.currentTarget.dataset.nextPhase
    if (!taskId || !nextPhase) return

    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/tasks/" + taskId + "/transition"), {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ nextPhase: nextPhase }),
    })
      .then(function (r) {
        if (!r.ok) throw new Error("transition failed: " + r.status)
        return r.json()
      })
      .then(function () {
        fetchTasks()
      })
      .catch(function (err) {
        console.error("handlePhaseTransition:", err)
      })
  }

  // ── Resize handler ────────────────────────────────────────────────
  window.addEventListener("resize", function () {
    if (state.fitAddon) {
      state.fitAddon.fit()
      sendTerminalResize()
    }
  })

  // ── Mobile back ───────────────────────────────────────────────────
  function handleMobileBack() {
    state.selectedTaskId = null
    disconnectTerminal()
    var panels = document.getElementById("panels")
    if (panels) panels.classList.remove("detail-active")

    // Reset right panel to empty state
    var emptyState = document.getElementById("empty-state")
    var detailView = document.getElementById("detail-view")
    if (emptyState) emptyState.style.display = ""
    if (detailView) detailView.style.display = "none"

    renderTaskList()
    renderTopBar()
    renderChatBar()
    renderAskBanner()
  }

  // ── Chat mode colors ────────────────────────────────────────────────
  var CHAT_MODES = {
    reply:     { icon: "\u21A9", color: "#e8a932", label: "Reply" },
    new:       { icon: "+", color: "#4ca8e8", label: "New task" },
    conductor: { icon: "\u25CE", color: "#8b8cf8", label: "Conductor" },
  }

  // ── Slash command definitions ─────────────────────────────────────
  var HUB_COMMANDS = [
    { cmd: "/new", desc: "Create new task (override reply)", group: "Hub" },
    { cmd: "/fork", desc: "Fork \u2192 new sibling task", group: "Hub" },
    { cmd: "/diff", desc: "View git diff for task", group: "Hub" },
    { cmd: "/approve", desc: "Approve and merge", group: "Hub" },
    { cmd: "/reject", desc: "Reject task changes", group: "Hub" },
    { cmd: "/status", desc: "All agent statuses", group: "Hub" },
    { cmd: "/sessions", desc: "List sessions for task", group: "Hub" },
    { cmd: "/conductor", desc: "Message conductor", group: "Hub" },
  ]
  var CLAUDE_COMMANDS = [
    { cmd: "/compact", desc: "Compact conversation context", group: "Claude Code" },
    { cmd: "/permissions", desc: "Toggle bypass mode", group: "Claude Code" },
    { cmd: "/memory", desc: "View/edit CLAUDE.md", group: "Claude Code" },
    { cmd: "/cost", desc: "Token usage this session", group: "Claude Code" },
    { cmd: "/clear", desc: "Clear conversation", group: "Claude Code" },
  ]
  var SKILL_COMMANDS = [
    { cmd: "/test", desc: "Run test suite", group: "Skills" },
    { cmd: "/lint", desc: "Run linter", group: "Skills" },
    { cmd: "/deploy", desc: "Deploy to staging", group: "Skills" },
  ]

  var GROUP_COLORS = {
    Hub: "var(--accent)",
    "Claude Code": "var(--purple)",
    Skills: "var(--green)",
  }

  // ── Chat mode detection ────────────────────────────────────────────
  function detectChatMode() {
    if (state.chatModeOverride) return state.chatModeOverride

    var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null

    var taskStatus = task ? effectiveAgentStatus(task) : "idle"
    if (state.activeView === "agents" && task && taskStatus !== "complete" && taskStatus !== "idle") {
      return {
        mode: "reply",
        label: task.id + "/" + task.phase,
        icon: "\u21A9",
        color: CHAT_MODES.reply.color,
        tmuxSession: task.tmuxSession,
        taskId: task.id,
        sessionPhase: task.phase,
        askQuestion: task.askQuestion,
      }
    }

    if (state.activeView === "conductor") {
      return {
        mode: "conductor",
        label: "Conductor",
        icon: "\u25CE",
        color: CHAT_MODES.conductor.color,
      }
    }

    var project = ""
    if (task) project = task.project
    else if (state.projectFilter) project = state.projectFilter

    return {
      mode: "new",
      label: project || "auto-route",
      icon: "+",
      color: CHAT_MODES.new.color,
      target: project,
    }
  }

  function renderChatBar() {
    var mode = detectChatMode()
    state.chatMode = mode

    var modeBtn = document.getElementById("chat-mode-btn")
    var modeIcon = document.getElementById("chat-mode-icon")
    var modeLabel = document.getElementById("chat-mode-label")
    var input = document.getElementById("chat-input")
    var hint = document.getElementById("chat-bar-hint")
    var sendBtn = document.getElementById("chat-send-btn")

    // Style mode button with color tint
    if (modeBtn) {
      modeBtn.style.background = mode.color + "12"
      modeBtn.style.borderColor = mode.color + "30"
      modeBtn.style.color = mode.color
    }
    if (modeIcon) { modeIcon.textContent = mode.icon; modeIcon.style.color = mode.color }
    if (modeLabel) { modeLabel.textContent = mode.label; modeLabel.style.color = mode.color }

    // Placeholder
    if (input) {
      if (mode.mode === "reply" && mode.askQuestion) {
        input.placeholder = "Answer: " + mode.askQuestion
      } else if (mode.mode === "reply") {
        input.placeholder = "Reply to " + (mode.taskId || "agent") + " / " + (mode.sessionPhase || "session") + "..."
      } else if (mode.mode === "new" && mode.target) {
        input.placeholder = "New task in " + mode.target + "..."
      } else if (mode.mode === "new") {
        input.placeholder = "Describe a task (conductor will route)..."
      } else if (mode.mode === "conductor") {
        input.placeholder = "Message conductor..."
      } else {
        input.placeholder = "Type a message..."
      }
    }

    // "via tmux send-keys" hint
    if (hint) {
      var tmuxTarget = mode.tmuxSession || "new session"
      hint.textContent = "via tmux send-keys \u2192 " + tmuxTarget
    }

    // Send button state
    updateSendButton()
  }

  function updateSendButton() {
    var input = document.getElementById("chat-input")
    var sendBtn = document.getElementById("chat-send-btn")
    if (!input || !sendBtn) return

    if (input.value.trim()) {
      sendBtn.classList.add("chat-send-btn--active")
    } else {
      sendBtn.classList.remove("chat-send-btn--active")
    }
  }

  // ── AskUserQuestion banner ─────────────────────────────────────────
  function renderAskBanner() {
    var existing = document.querySelector(".ask-banner")
    if (existing) existing.remove()

    var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null
    if (!task || effectiveAgentStatus(task) !== "waiting" || !task.askQuestion) return

    var banner = el("div", "ask-banner")
    var icon = el("span", "ask-banner-icon", "\u25D0")
    banner.appendChild(icon)

    var msgSpan = el("span", null, "Agent is asking: ")
    var qSpan = el("span", null, task.askQuestion)
    qSpan.style.color = "var(--text)"
    banner.appendChild(msgSpan)
    banner.appendChild(qSpan)

    var chatBar = document.getElementById("chat-bar")
    if (chatBar && chatBar.parentNode) {
      chatBar.parentNode.insertBefore(banner, chatBar)
    }
  }

  // ── Slash command palette ──────────────────────────────────────────
  function renderSlashPalette() {
    var existing = document.querySelector(".slash-palette")
    if (existing) existing.remove()

    var input = document.getElementById("chat-input")
    var value = input ? input.value : ""
    if (!value.startsWith("/")) return

    var mode = state.chatMode || detectChatMode()
    var isProjectMode = mode.mode === "reply" || (mode.mode === "new" && mode.target)

    var allCommands = HUB_COMMANDS.slice()
    if (isProjectMode) {
      allCommands = allCommands.concat(CLAUDE_COMMANDS)
      allCommands = allCommands.concat(SKILL_COMMANDS)
    }

    // Filter by typed text
    var filter = value.toLowerCase()
    var filtered = []
    for (var i = 0; i < allCommands.length; i++) {
      if (allCommands[i].cmd.includes(filter)) filtered.push(allCommands[i])
    }
    if (filtered.length === 0) return

    var palette = el("div", "slash-palette open")

    // Group commands
    var groups = []
    var groupMap = {}
    for (var j = 0; j < filtered.length; j++) {
      var g = filtered[j].group
      if (!groupMap[g]) {
        groupMap[g] = []
        groups.push(g)
      }
      groupMap[g].push(filtered[j])
    }

    for (var k = 0; k < groups.length; k++) {
      var groupName = groups[k]
      palette.appendChild(el("div", "slash-group-header", groupName))

      var cmds = groupMap[groupName]
      for (var c = 0; c < cmds.length; c++) {
        var cmdBtn = el("button", "slash-command")
        var nameSpan = el("span", "slash-command-name", cmds[c].cmd)
        nameSpan.style.color = GROUP_COLORS[groupName] || "var(--text)"
        cmdBtn.appendChild(nameSpan)
        cmdBtn.appendChild(el("span", "slash-command-desc", cmds[c].desc))
        cmdBtn.dataset.cmd = cmds[c].cmd
        cmdBtn.addEventListener("click", function (e) {
          var cmdVal = e.currentTarget.dataset.cmd
          if (input) { input.value = cmdVal + " "; input.focus() }
          closeSlashPalette()
        })
        palette.appendChild(cmdBtn)
      }
    }

    var chatBar = document.getElementById("chat-bar")
    if (chatBar) chatBar.appendChild(palette)
  }

  function closeSlashPalette() {
    var p = document.querySelector(".slash-palette")
    if (p) p.remove()
  }

  // ── Chat mode override menu ────────────────────────────────────────
  function renderModeMenu() {
    var existing = document.querySelector(".chat-mode-menu")
    if (existing) existing.remove()

    var menu = el("div", "chat-mode-menu open")
    var mode = state.chatMode || detectChatMode()

    // Header
    menu.appendChild(el("div", "chat-mode-menu-header", "Switch context"))

    // "Back to auto" if overridden (show first)
    if (state.chatModeOverride) {
      var backOpt = createModeOption("\u2190", "var(--text-dim)", "\u2190 Back to: auto", "Use auto-detected context")
      backOpt.dataset.mode = "auto"
      backOpt.addEventListener("click", handleModeSelect)
      menu.appendChild(backOpt)
    }

    // "New in {project}" for each project (skip current if reply mode)
    for (var i = 0; i < state.projects.length; i++) {
      var proj = state.projects[i]
      if (mode.mode === "reply" && proj.name === mode.target) continue
      var opt = createModeOption("+", CHAT_MODES.new.color, "+ New in " + proj.name, "New task in " + proj.name)
      opt.dataset.mode = "new"
      opt.dataset.project = proj.name
      opt.addEventListener("click", handleModeSelect)
      menu.appendChild(opt)
    }

    // "New (auto-route)"
    var autoOpt = createModeOption("+", CHAT_MODES.new.color, "+ New (auto-route)", "Conductor picks project")
    autoOpt.dataset.mode = "new"
    autoOpt.dataset.project = ""
    autoOpt.addEventListener("click", handleModeSelect)
    menu.appendChild(autoOpt)

    // "Message conductor"
    var condOpt = createModeOption("\u25CE", CHAT_MODES.conductor.color, "\u25CE Message conductor", "Orchestration commands")
    condOpt.dataset.mode = "conductor"
    condOpt.dataset.project = ""
    condOpt.addEventListener("click", handleModeSelect)
    menu.appendChild(condOpt)

    var chatBar = document.getElementById("chat-bar")
    if (chatBar) chatBar.appendChild(menu)

    setTimeout(function () {
      document.addEventListener("click", closeModeMenu)
    }, 0)
  }

  function createModeOption(iconText, iconColor, label, desc) {
    var opt = el("button", "chat-mode-option")
    var icon = el("span", "chat-mode-option-icon", iconText)
    icon.style.color = iconColor
    opt.appendChild(icon)
    var textWrap = el("div", "chat-mode-option-text")
    textWrap.appendChild(el("div", "chat-mode-option-label", label))
    if (desc) textWrap.appendChild(el("div", "chat-mode-option-desc", desc))
    opt.appendChild(textWrap)
    return opt
  }

  function handleModeSelect(e) {
    var btn = e.currentTarget
    if (btn.dataset.mode === "auto") {
      state.chatModeOverride = null
    } else if (btn.dataset.mode === "conductor") {
      state.chatModeOverride = {
        mode: "conductor",
        label: "\u25CE Conductor",
        icon: "\u25CE",
        color: CHAT_MODES.conductor.color,
      }
    } else {
      state.chatModeOverride = {
        mode: btn.dataset.mode,
        label: btn.dataset.project ? "+ " + btn.dataset.project : "+ auto-route",
        icon: "+",
        color: CHAT_MODES.new.color,
        target: btn.dataset.project,
      }
    }
    closeModeMenu()
    renderChatBar()
  }

  function closeModeMenu() {
    var menu = document.querySelector(".chat-mode-menu")
    if (menu) menu.remove()
    document.removeEventListener("click", closeModeMenu)
  }

  // ── File upload ──────────────────────────────────────────────────
  var UPLOAD_CHUNK_SIZE = 64 * 1024 // 64 KB

  function uploadFile(file) {
    if (!state.selectedTaskId) return
    var sessionId = state.selectedTaskId

    var protocol = location.protocol === "https:" ? "wss:" : "ws:"
    var url = protocol + "//" + location.host + "/ws/upload/" + encodeURIComponent(sessionId)
    var ws = new WebSocket(url)

    var progressBar = document.getElementById("upload-progress")
    var progressFill = document.getElementById("upload-progress-fill")
    if (!progressBar) {
      var chatBar = document.getElementById("chat-bar")
      if (chatBar) {
        progressBar = el("div", "upload-progress")
        progressBar.id = "upload-progress"
        progressFill = el("div", "upload-progress-fill")
        progressFill.id = "upload-progress-fill"
        progressBar.appendChild(progressFill)
        chatBar.parentNode.insertBefore(progressBar, chatBar)
      }
    }
    if (progressBar) progressBar.style.display = ""
    if (progressFill) progressFill.style.width = "0%"

    ws.onopen = function () {
      ws.send(JSON.stringify({ type: "start", filename: file.name, size: file.size }))
      sendChunks(ws, file, 0)
    }

    ws.onmessage = function (e) {
      try {
        var msg = JSON.parse(e.data)
        if (msg.type === "progress" && progressFill && msg.total > 0) {
          var pct = Math.min(100, Math.round((msg.received / msg.total) * 100))
          progressFill.style.width = pct + "%"
        }
        if (msg.type === "complete") {
          if (progressFill) progressFill.style.width = "100%"
          setTimeout(function () {
            if (progressBar) progressBar.style.display = "none"
          }, 1500)
        }
        if (msg.type === "error") {
          console.error("Upload error:", msg.message)
          if (progressBar) progressBar.style.display = "none"
        }
      } catch (_) {}
    }

    ws.onerror = function () {
      if (progressBar) progressBar.style.display = "none"
    }
  }

  function sendChunks(ws, file, offset) {
    if (offset >= file.size) {
      ws.send(JSON.stringify({ type: "end" }))
      return
    }
    var end = Math.min(offset + UPLOAD_CHUNK_SIZE, file.size)
    var slice = file.slice(offset, end)
    var reader = new FileReader()
    reader.onload = function () {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(reader.result)
        sendChunks(ws, file, end)
      }
    }
    reader.onerror = function () {
      try { ws.close() } catch (_) {}
    }
    reader.readAsArrayBuffer(slice)
  }

  function handleUploadFiles(files) {
    for (var i = 0; i < files.length; i++) {
      uploadFile(files[i])
    }
  }

  function sendChatMessage() {
    var input = document.getElementById("chat-input")
    if (!input) return
    var text = input.value.trim()
    if (!text) return

    var mode = state.chatMode || detectChatMode()

    if (mode.mode === "reply" && state.selectedTaskId) {
      sendTaskInput(state.selectedTaskId, text)
    } else if (mode.mode === "conductor") {
      sendConductorMessage(text)
    } else {
      openNewTaskModalWithDescription(text)
    }
    input.value = ""
    updateSendButton()
    closeSlashPalette()
  }

  function sendTaskInput(taskId, text) {
    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/tasks/" + taskId + "/input"), {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ input: text }),
    })
      .then(function (r) {
        if (!r.ok) throw new Error("send failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        if (data && data.status === "delivered") {
          // Refresh messages after a delay to pick up the sent input and any response.
          var task = findTask(taskId)
          var msgSessionId = getSessionIdForMessages(task)
          if (msgSessionId) {
            // Clear cache so reload is forced.
            state.lastLoadedMessagesSession = null
            setTimeout(function () { loadSessionMessages(msgSessionId) }, 1500)
            setTimeout(function () { loadSessionMessages(msgSessionId) }, 5000)
          }
        }
      })
      .catch(function (err) {
        console.error("sendTaskInput:", err)
      })
  }

  function sendConductorMessage(text) {
    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/conductor/input"), {
      method: "POST",
      headers: headers,
      body: JSON.stringify({ input: text }),
    })
      .then(function (r) {
        if (!r.ok) throw new Error("conductor send failed: " + r.status)
      })
      .catch(function (err) {
        console.error("sendConductorMessage:", err)
      })
  }

  // ── Utilities ─────────────────────────────────────────────────────
  function formatDuration(isoDate) {
    if (!isoDate) return "\u2014"
    var created = new Date(isoDate)
    if (isNaN(created.getTime())) return "\u2014"

    var ms = Date.now() - created.getTime()
    if (ms < 0) ms = 0

    var seconds = Math.floor(ms / 1000)
    if (seconds < 60) return seconds + "s"

    var minutes = Math.floor(seconds / 60)
    if (minutes < 60) return minutes + "m"

    var hours = Math.floor(minutes / 60)
    var remainMinutes = minutes % 60
    if (hours < 24) return hours + "h " + remainMinutes + "m"

    var days = Math.floor(hours / 24)
    return days + "d " + (hours % 24) + "h"
  }

  // ── New Task modal ────────────────────────────────────────────────
  var newTaskModal = document.getElementById("new-task-modal")
  var newTaskBackdrop = document.getElementById("new-task-backdrop")
  var newTaskProject = document.getElementById("new-task-project")
  var newTaskDesc = document.getElementById("new-task-desc")
  var newTaskPhase = document.getElementById("new-task-phase")
  var newTaskSubmit = document.getElementById("new-task-submit")
  var routeSuggestion = document.getElementById("route-suggestion")

  function openNewTaskModal() {
    clearChildren(newTaskProject)
    var hasProjects = state.projects.length > 0
    for (var i = 0; i < state.projects.length; i++) {
      var opt = document.createElement("option")
      opt.value = state.projects[i].name
      opt.textContent = state.projects[i].name
      newTaskProject.appendChild(opt)
    }
    if (!hasProjects) {
      var placeholder = document.createElement("option")
      placeholder.value = ""
      placeholder.textContent = "No projects configured"
      placeholder.disabled = true
      placeholder.selected = true
      newTaskProject.appendChild(placeholder)
    }
    // Add "+ Add Project..." option
    var addOpt = document.createElement("option")
    addOpt.value = "__add_project__"
    addOpt.textContent = "+ Add Project..."
    newTaskProject.appendChild(addOpt)

    if (newTaskDesc) newTaskDesc.value = ""
    if (newTaskPhase) newTaskPhase.value = "execute"
    if (newTaskSubmit) newTaskSubmit.disabled = !hasProjects
    if (routeSuggestion) routeSuggestion.textContent = ""

    if (newTaskModal) newTaskModal.classList.add("open")
    if (newTaskBackdrop) newTaskBackdrop.classList.add("open")
    if (newTaskModal) newTaskModal.setAttribute("aria-hidden", "false")
    if (newTaskDesc) newTaskDesc.focus()
  }

  function openNewTaskModalWithDescription(text) {
    openNewTaskModal()
    if (newTaskDesc) newTaskDesc.value = text
    suggestProject(text)
  }

  function closeNewTaskModal() {
    if (routeTimer) { clearTimeout(routeTimer); routeTimer = null }
    if (newTaskModal) newTaskModal.classList.remove("open")
    if (newTaskBackdrop) newTaskBackdrop.classList.remove("open")
    if (newTaskModal) newTaskModal.setAttribute("aria-hidden", "true")
  }

  function submitNewTask() {
    var project = newTaskProject ? newTaskProject.value : ""
    var description = newTaskDesc ? newTaskDesc.value.trim() : ""
    var phase = newTaskPhase ? newTaskPhase.value : "execute"

    if (!project || !description) return

    var body = JSON.stringify({ project: project, description: description, phase: phase })
    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/tasks"), {
      method: "POST",
      headers: headers,
      body: body,
    })
      .then(function (r) {
        if (!r.ok) throw new Error("create failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        closeNewTaskModal()
        fetchTasks()
        if (data.task && data.task.id) selectTask(data.task.id)
      })
      .catch(function (err) {
        console.error("submitNewTask:", err)
      })
  }

  // ── Auto-suggest project via routing ──────────────────────────────
  var routeTimer = null

  function suggestProject(message) {
    if (routeTimer) clearTimeout(routeTimer)
    if (!message || message.length < 5) {
      if (routeSuggestion) routeSuggestion.textContent = ""
      return
    }

    routeTimer = setTimeout(function () {
      routeTimer = null
      var headers = authHeaders()
      headers["Content-Type"] = "application/json"

      fetch(apiPathWithToken("/api/route"), {
        method: "POST",
        headers: headers,
        body: JSON.stringify({ message: message }),
      })
        .then(function (r) {
          if (!r.ok) return null
          return r.json()
        })
        .then(function (data) {
          if (!data || !data.project) {
            if (routeSuggestion) {
              routeSuggestion.textContent = "No matching project"
              routeSuggestion.className = "route-suggestion route-suggestion-muted"
            }
            return
          }
          if (routeSuggestion) {
            routeSuggestion.textContent =
              "Suggested: " + data.project +
              " (" + Math.round(data.confidence * 100) + "% match)"
            routeSuggestion.className = "route-suggestion"
          }
          if (newTaskProject) {
            for (var i = 0; i < newTaskProject.options.length; i++) {
              if (newTaskProject.options[i].value === data.project) {
                newTaskProject.selectedIndex = i
                break
              }
            }
          }
        })
        .catch(function () {
          if (routeSuggestion) routeSuggestion.textContent = ""
        })
    }, 300)
  }

  // ── Messages tab ────────────────────────────────────────────────
  function loadSessionMessages(sessionId) {
    if (!sessionId) return
    state.lastLoadedMessagesSession = sessionId

    var url = apiPathWithToken("/api/messages/" + encodeURIComponent(sessionId))
    fetch(url, { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("messages fetch failed: " + r.status)
        return r.json()
      })
      .then(function (data) {
        var messages = data && data.messages ? data.messages : []
        renderMessages(messages)
      })
      .catch(function (err) {
        console.error("loadSessionMessages:", err)
        var container = document.getElementById("messages-container")
        if (container) {
          clearChildren(container)
          container.appendChild(el("div", "terminal-placeholder", "Failed to load messages."))
        }
      })
  }

  function renderMessages(messages) {
    var container = document.getElementById("messages-container")
    if (!container) return

    clearChildren(container)

    if (!messages || messages.length === 0) {
      container.appendChild(el("div", "terminal-placeholder", "No messages yet."))
      return
    }

    for (var i = 0; i < messages.length; i++) {
      var msg = messages[i]
      var role = msg.role || msg.type || "unknown"
      var variant = role === "user" ? "--user" : "--assistant"
      var msgBlock = el("div", "message-block message-block" + variant)

      // Role label
      var roleLabel = el("div", "message-role")
      roleLabel.textContent = role.charAt(0).toUpperCase() + role.slice(1)
      msgBlock.appendChild(roleLabel)

      // Message content text
      if (msg.content) {
        var contentDiv = el("div", "message-content")
        contentDiv.textContent = msg.content
        msgBlock.appendChild(contentDiv)
      }

      // Tool result rendering
      if (msg.toolName) {
        try {
          var toolEl = ToolRenderers.render(
            msg.toolName,
            msg.toolInput || null,
            msg.toolResult || null,
            msg.augment || null
          )
          msgBlock.appendChild(toolEl)
        } catch (err) {
          console.error("ToolRenderers.render failed for", msg.toolName, err)
          msgBlock.appendChild(el("div", "tool-block", "[tool: " + msg.toolName + "]"))
        }
      }

      container.appendChild(msgBlock)
    }

    // Scroll to bottom
    container.scrollTop = container.scrollHeight
  }

  function switchDetailTab(tabName) {
    var terminalContainer = document.getElementById("terminal-container")
    var messagesContainer = document.getElementById("messages-container")
    var tabs = document.querySelectorAll(".detail-tab")

    for (var i = 0; i < tabs.length; i++) {
      if (tabs[i].dataset.tab === tabName) {
        tabs[i].classList.add("detail-tab--active")
      } else {
        tabs[i].classList.remove("detail-tab--active")
      }
    }

    var toolbar = document.getElementById("terminal-toolbar")

    if (tabName === "terminal") {
      if (terminalContainer) terminalContainer.style.display = ""
      if (messagesContainer) messagesContainer.style.display = "none"
      if (toolbar && state.terminal) toolbar.style.display = ""
      // Re-fit terminal when switching back and sync size with server
      if (state.fitAddon) {
        setTimeout(function () {
          state.fitAddon.fit()
          sendTerminalResize()
          updateTerminalToolbar()
        }, 50)
      }
    } else if (tabName === "messages") {
      if (terminalContainer) terminalContainer.style.display = "none"
      if (messagesContainer) messagesContainer.style.display = ""
      if (toolbar) toolbar.style.display = "none"

      // Load messages for the selected task (skip if already loaded for this session)
      if (state.selectedTaskId) {
        var task = findTask(state.selectedTaskId)
        var msgSessionId = getSessionIdForMessages(task)
        if (msgSessionId && msgSessionId !== state.lastLoadedMessagesSession) {
          loadSessionMessages(msgSessionId)
        }
      }
    }
  }

  // ── Add Project modal ────────────────────────────────────────────
  var addProjectModal = document.getElementById("add-project-modal")
  var addProjectBackdrop = document.getElementById("add-project-backdrop")
  var addProjectRepo = document.getElementById("add-project-repo")
  var addProjectName = document.getElementById("add-project-name")
  var addProjectPath = document.getElementById("add-project-path")
  var addProjectKeywords = document.getElementById("add-project-keywords")
  var addProjectContainer = document.getElementById("add-project-container")        // <select>
  var addProjectContainerCustom = document.getElementById("add-project-container-custom")  // fallback text input
  var addProjectStatus = document.getElementById("add-project-status")
  var addProjectImage = document.getElementById("add-project-image")
  var addProjectCpu = document.getElementById("add-project-cpu")
  var addProjectMem = document.getElementById("add-project-mem")

  function setProjectStatus(msg, isError) {
    if (!addProjectStatus) return
    addProjectStatus.textContent = msg
    addProjectStatus.className = "modal-status" + (msg ? (isError ? " modal-status--error" : " modal-status--ok") : "")
  }

  function populateContainerDropdown() {
    if (!addProjectContainer) return
    // Keep first "Select..." option, clear rest
    while (addProjectContainer.options.length > 1) addProjectContainer.remove(1)

    fetch(apiPathWithToken("/api/workspaces"), { headers: authHeaders() })
      .then(function (r) {
        if (!r.ok) throw new Error("fetch failed")
        return r.json()
      })
      .then(function (data) {
        var ws = data.workspaces || []
        var hasContainers = false
        for (var i = 0; i < ws.length; i++) {
          if (ws[i].container) {
            var opt = document.createElement("option")
            opt.value = ws[i].container
            opt.textContent = ws[i].name + " (" + ws[i].container + ")"
            addProjectContainer.appendChild(opt)
            hasContainers = true
          }
        }
        // Add "Custom..." option to allow manual entry
        var customOpt = document.createElement("option")
        customOpt.value = "__custom__"
        customOpt.textContent = hasContainers ? "Other (enter manually)..." : "No containers found — enter manually..."
        addProjectContainer.appendChild(customOpt)
      })
      .catch(function () {
        // API unavailable — show fallback text input instead
        if (addProjectContainer) addProjectContainer.style.display = "none"
        if (addProjectContainerCustom) addProjectContainerCustom.style.display = ""
      })
  }

  function openAddProjectModal() {
    if (addProjectRepo) addProjectRepo.value = ""
    if (addProjectName) addProjectName.value = ""
    if (addProjectPath) addProjectPath.value = ""
    if (addProjectKeywords) addProjectKeywords.value = ""
    if (addProjectContainer) addProjectContainer.value = ""
    if (addProjectContainerCustom) addProjectContainerCustom.value = ""
    if (addProjectImage) addProjectImage.value = ""
    if (addProjectCpu) addProjectCpu.value = "2"
    if (addProjectMem) addProjectMem.value = "2"
    if (addProjectModal) addProjectModal.dataset.template = ""
    setProjectStatus("")
    // Reset radio to "none"
    var radios = document.querySelectorAll('input[name="container-mode"]')
    for (var i = 0; i < radios.length; i++) {
      radios[i].checked = radios[i].value === "none"
    }
    updateContainerFields()
    populateTemplatePicker()
    if (addProjectModal) addProjectModal.classList.add("open")
    if (addProjectBackdrop) addProjectBackdrop.classList.add("open")
    if (addProjectModal) addProjectModal.setAttribute("aria-hidden", "false")
    if (addProjectRepo) addProjectRepo.focus()
  }

  function closeAddProjectModal() {
    if (addProjectModal) addProjectModal.classList.remove("open")
    if (addProjectBackdrop) addProjectBackdrop.classList.remove("open")
    if (addProjectModal) addProjectModal.setAttribute("aria-hidden", "true")
    setProjectStatus("")
  }

  function getContainerMode() {
    var radios = document.querySelectorAll('input[name="container-mode"]')
    for (var i = 0; i < radios.length; i++) {
      if (radios[i].checked) return radios[i].value
    }
    return "none"
  }

  function updateContainerFields() {
    var mode = getContainerMode()
    var existingEl = document.getElementById("container-fields-existing")
    var provisionEl = document.getElementById("container-fields-provision")
    if (existingEl) existingEl.style.display = mode === "existing" ? "" : "none"
    if (provisionEl) provisionEl.style.display = mode === "provision" ? "" : "none"

    // Populate container dropdown when "existing" is selected
    if (mode === "existing") {
      // Reset: show dropdown, hide custom input
      if (addProjectContainer) addProjectContainer.style.display = ""
      if (addProjectContainerCustom) addProjectContainerCustom.style.display = "none"
      populateContainerDropdown()
    }
  }

  function submitAddProject() {
    var repo = addProjectRepo ? addProjectRepo.value.trim() : ""
    var name = addProjectName ? addProjectName.value.trim() : ""
    var path = addProjectPath ? addProjectPath.value.trim() : ""
    var keywords = addProjectKeywords ? addProjectKeywords.value.trim() : ""
    var mode = getContainerMode()

    if (!repo && !name) {
      setProjectStatus("Repo or name is required", true)
      return
    }

    var body = { repo: repo, name: name, path: path }
    if (keywords) body.keywords = keywords.split(",").map(function (k) { return k.trim() }).filter(Boolean)

    // Include template if one was selected.
    var selectedTemplate = addProjectModal ? (addProjectModal.dataset.template || "") : ""
    if (selectedTemplate) body.template = selectedTemplate

    if (mode === "existing") {
      // Read from dropdown or fallback custom input
      var dropVal = addProjectContainer ? addProjectContainer.value : ""
      if (dropVal === "__custom__" || addProjectContainer.style.display === "none") {
        body.container = addProjectContainerCustom ? addProjectContainerCustom.value.trim() : ""
      } else {
        body.container = dropVal
      }
    } else if (mode === "provision") {
      body.image = addProjectImage ? addProjectImage.value.trim() : ""
      if (!body.image) {
        setProjectStatus("Docker image is required for auto-provision", true)
        return
      }
      body.cpuLimit = parseFloat(addProjectCpu ? addProjectCpu.value : "2") || 2
      body.memoryLimit = Math.round((parseFloat(addProjectMem ? addProjectMem.value : "2") || 2) * 1024 * 1024 * 1024)
    }

    // Disable button while submitting
    var submitBtn = document.getElementById("add-project-submit")
    if (submitBtn) submitBtn.disabled = true
    setProjectStatus("Creating project...", false)

    var headers = authHeaders()
    headers["Content-Type"] = "application/json"

    fetch(apiPathWithToken("/api/projects"), {
      method: "POST",
      headers: headers,
      body: JSON.stringify(body),
    })
      .then(function (r) {
        if (!r.ok) throw new Error("create project failed: " + r.status)
        return r.json()
      })
      .then(function () {
        closeAddProjectModal()
        fetchProjects()
        fetchTasks()
      })
      .catch(function (err) {
        console.error("submitAddProject:", err)
        setProjectStatus("Failed to create project — is the backend running?", true)
      })
      .finally(function () {
        if (submitBtn) submitBtn.disabled = false
      })
  }

  // Auto-derive name from repo
  if (addProjectRepo) {
    addProjectRepo.addEventListener("input", function () {
      var val = addProjectRepo.value.trim()
      var parts = val.split("/")
      var derived = parts[parts.length - 1] || ""
      if (addProjectName) addProjectName.value = derived
      if (addProjectPath && derived) addProjectPath.value = "~/projects/" + derived
    })
  }

  // ── Event listeners ───────────────────────────────────────────────

  // Sidebar view icons
  var sidebarIcons = document.querySelectorAll(".sidebar-icon[data-view]")
  for (var si = 0; si < sidebarIcons.length; si++) {
    sidebarIcons[si].addEventListener("click", handleSidebarClick)
  }

  // New task button (sidebar +)
  var newTaskBtn = document.getElementById("new-task-btn")
  if (newTaskBtn) newTaskBtn.addEventListener("click", openNewTaskModal)

  // Modal controls
  var newTaskClose = document.getElementById("new-task-close")
  var newTaskCancel = document.getElementById("new-task-cancel")
  if (newTaskClose) newTaskClose.addEventListener("click", closeNewTaskModal)
  if (newTaskCancel) newTaskCancel.addEventListener("click", closeNewTaskModal)
  if (newTaskBackdrop) newTaskBackdrop.addEventListener("click", closeNewTaskModal)
  if (newTaskSubmit) newTaskSubmit.addEventListener("click", submitNewTask)
  if (newTaskDesc) {
    newTaskDesc.addEventListener("input", function () {
      suggestProject(newTaskDesc.value.trim())
    })
  }

  // Add project modal controls
  var addProjectBtn = document.getElementById("add-project-btn")
  if (addProjectBtn) addProjectBtn.addEventListener("click", openAddProjectModal)
  var addProjectClose = document.getElementById("add-project-close")
  var addProjectCancel = document.getElementById("add-project-cancel")
  var addProjectSubmitBtn = document.getElementById("add-project-submit")
  if (addProjectClose) addProjectClose.addEventListener("click", closeAddProjectModal)
  if (addProjectCancel) addProjectCancel.addEventListener("click", closeAddProjectModal)
  if (addProjectBackdrop) addProjectBackdrop.addEventListener("click", closeAddProjectModal)
  if (addProjectSubmitBtn) addProjectSubmitBtn.addEventListener("click", submitAddProject)

  // Container mode radio changes
  var containerModeRadios = document.querySelectorAll('input[name="container-mode"]')
  for (var cmi = 0; cmi < containerModeRadios.length; cmi++) {
    containerModeRadios[cmi].addEventListener("change", updateContainerFields)
  }

  // Container dropdown: show custom text input when "Other" is selected
  if (addProjectContainer) {
    addProjectContainer.addEventListener("change", function () {
      if (addProjectContainer.value === "__custom__") {
        if (addProjectContainerCustom) {
          addProjectContainerCustom.style.display = ""
          addProjectContainerCustom.focus()
        }
      } else {
        if (addProjectContainerCustom) addProjectContainerCustom.style.display = "none"
      }
    })
  }

  // New task project dropdown: intercept "+ Add Project..." selection
  if (newTaskProject) {
    newTaskProject.addEventListener("change", function () {
      if (newTaskProject.value === "__add_project__") {
        closeNewTaskModal()
        openAddProjectModal()
      }
    })
  }

  // Chat bar
  var chatModeBtn = document.getElementById("chat-mode-btn")
  if (chatModeBtn) {
    chatModeBtn.addEventListener("click", function (e) {
      e.stopPropagation()
      var existing = document.querySelector(".chat-mode-menu")
      if (existing) { closeModeMenu(); return }
      renderModeMenu()
    })
  }
  var chatSendBtn = document.getElementById("chat-send-btn")
  var chatInput = document.getElementById("chat-input")
  if (chatSendBtn) chatSendBtn.addEventListener("click", sendChatMessage)
  if (chatInput) {
    chatInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault()
        sendChatMessage()
      }
      if (e.key === "Escape") {
        closeSlashPalette()
      }
    })
    chatInput.addEventListener("input", function () {
      updateSendButton()
      if (chatInput.value.startsWith("/")) {
        renderSlashPalette()
      } else {
        closeSlashPalette()
      }
    })
    // Paste handler: detect files/images in clipboard
    chatInput.addEventListener("paste", function (e) {
      var items = (e.clipboardData || {}).items
      if (!items) return
      var files = []
      for (var pi = 0; pi < items.length; pi++) {
        if (items[pi].kind === "file") files.push(items[pi].getAsFile())
      }
      if (files.length > 0) {
        e.preventDefault()
        handleUploadFiles(files)
      }
    })
  }

  // Drag-and-drop upload on chat bar
  var chatBarEl = document.getElementById("chat-bar")
  if (chatBarEl) {
    chatBarEl.addEventListener("dragover", function (e) {
      e.preventDefault()
      chatBarEl.classList.add("upload-dropzone")
    })
    chatBarEl.addEventListener("dragleave", function () {
      chatBarEl.classList.remove("upload-dropzone")
    })
    chatBarEl.addEventListener("drop", function (e) {
      e.preventDefault()
      chatBarEl.classList.remove("upload-dropzone")
      if (e.dataTransfer && e.dataTransfer.files.length > 0) {
        handleUploadFiles(e.dataTransfer.files)
      }
    })

    // Attachment button: opens native file picker
    var attachBtn = el("button", "chat-attach-btn")
    attachBtn.type = "button"
    attachBtn.setAttribute("aria-label", "Attach file")
    attachBtn.textContent = "\uD83D\uDCCE" // paperclip emoji
    var chatInner = chatBarEl.querySelector(".chat-bar-inner") || chatBarEl
    var chatInput = chatInner.querySelector(".chat-input")
    if (chatInput) chatInner.insertBefore(attachBtn, chatInput)
    attachBtn.addEventListener("click", function () {
      var input = document.createElement("input")
      input.type = "file"
      input.multiple = true
      input.onchange = function () {
        if (input.files && input.files.length > 0) {
          handleUploadFiles(input.files)
        }
      }
      input.click()
    })
  }

  // Mobile bottom nav
  var mobileNavItems = document.querySelectorAll(".mobile-nav-item[data-view]")
  for (var mi = 0; mi < mobileNavItems.length; mi++) {
    mobileNavItems[mi].addEventListener("click", function (e) {
      e.preventDefault()
      var view = e.currentTarget.dataset.view
      if (!view) return
      state.activeView = view

      // Update active state on mobile nav
      var items = document.querySelectorAll(".mobile-nav-item[data-view]")
      for (var n = 0; n < items.length; n++) {
        if (items[n].dataset.view === view) {
          items[n].classList.add("mobile-nav-item--active")
        } else {
          items[n].classList.remove("mobile-nav-item--active")
        }
      }

      renderSidebar()
      renderTopBar()
      renderView()
    })
  }

  // Escape key
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      if (addProjectModal && addProjectModal.classList.contains("open")) {
        closeAddProjectModal()
      } else if (newTaskModal && newTaskModal.classList.contains("open")) {
        closeNewTaskModal()
      } else if (state.selectedTaskId) {
        handleMobileBack()
      }
    }
  })

  // Detail tabs (Terminal / Messages)
  var detailTabs = document.querySelectorAll(".detail-tab")
  for (var dt = 0; dt < detailTabs.length; dt++) {
    detailTabs[dt].addEventListener("click", function (e) {
      var tabName = e.currentTarget.dataset.tab
      if (tabName) switchDetailTab(tabName)
    })
  }

  // ── Init ──────────────────────────────────────────────────────────
  renderSidebar()
  renderTopBar()
  renderView()
  renderChatBar()
  fetchTasks()
  fetchProjects()
  fetchMenuData()

  // ── ConnectionManager (WebSocket-based event bus) ───────────────
  ;(function initConnectionManager() {
    var wsProto = (location.protocol === "https:") ? "wss:" : "ws:"
    var wsUrl = wsProto + "//" + location.host + "/ws/events"
    var token = state.authToken
    if (token) wsUrl += "?token=" + encodeURIComponent(token)

    var cm = new ConnectionManager(wsUrl)

    // Subscribe to session updates
    cm.subscribe("sessions", function () {
      fetchMenuData()
      fetchTasks()
    })

    // Subscribe to task updates
    cm.subscribe("tasks", function () {
      if (typeof fetchTasks === "function") fetchTasks()
    })

    // Update connection bar on state changes
    cm.on("stateChange", function (newState) {
      var bar = document.getElementById("connection-bar")
      if (!bar) return
      bar.className = "connection-bar"
      if (newState === "connected") {
        bar.classList.add("connection-bar--connected")
      } else if (newState === "reconnecting") {
        bar.classList.add("connection-bar--reconnecting")
      } else {
        bar.classList.add("connection-bar--disconnected")
      }
    })

    // Refetch data after reconnect
    cm.on("reconnect", function () {
      fetchMenuData()
      fetchTasks()
      fetchProjects()
    })

    cm.connect()
  })()
})()
