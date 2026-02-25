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
    fitAddon: null,
    chatMode: null,
    chatModeOverride: null,
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

  var PHASES = ["brainstorm", "plan", "execute", "review"]
  var PHASE_DOT_LABELS = { brainstorm: "B", plan: "P", execute: "E", review: "R" }

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
      })
  }

  function findTask(id) {
    for (var i = 0; i < state.tasks.length; i++) {
      if (state.tasks[i].id === id) return state.tasks[i]
    }
    return null
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
    if (!countEl) return
    var active = 0
    for (var i = 0; i < state.tasks.length; i++) {
      if (state.tasks[i].status !== "done") active++
    }
    countEl.textContent = active
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
    renderSidebar()
    renderChatBar()
  }

  // ── Filter bar ────────────────────────────────────────────────────
  function renderFilterBar() {
    var filterBar = document.getElementById("filter-bar")
    if (!filterBar) return

    clearChildren(filterBar)

    // "All" pill
    var allPill = el("button", "filter-pill" + (state.projectFilter === "" ? " filter-pill--active" : ""), "All")
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
    var s = task.agentStatus || ""
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

    // Left border color from task status
    var borderColor = TASK_STATUS_COLORS[task.status] || "var(--text-dim)"
    card.style.borderLeftColor = borderColor

    // Top row: project name + task id
    var top = el("div", "agent-card-top")
    top.appendChild(el("span", "agent-card-project", task.project || "\u2014"))
    top.appendChild(el("span", "agent-card-id", task.id))
    card.appendChild(top)

    // Description
    if (task.description) {
      card.appendChild(el("div", "agent-card-desc", task.description))
    }

    // Footer: status badge + mini session chain
    var footer = el("div", "agent-card-footer")
    footer.appendChild(createAgentStatusBadge(task.agentStatus))

    // Ask badge if waiting with question
    if (task.agentStatus === "waiting" && task.askQuestion) {
      var askBadge = el("span", "ask-badge", "\u25D0 INPUT")
      footer.appendChild(askBadge)
    }

    // Mini session chain
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

  // ── Mini session chain (in card footer) ───────────────────────────
  function createMiniSessionChain(task) {
    var chain = el("div", "mini-session-chain")

    // Use sessions array if available, otherwise fall back to phase
    var phases = []
    if (task.sessions && task.sessions.length > 0) {
      for (var i = 0; i < task.sessions.length; i++) {
        phases.push({
          phase: task.sessions[i].phase,
          status: task.sessions[i].status,
        })
      }
    } else if (task.phase) {
      var currentIdx = PHASES.indexOf(task.phase)
      for (var j = 0; j < PHASES.length; j++) {
        phases.push({
          phase: PHASES[j],
          status: j < currentIdx ? "complete" : (j === currentIdx ? "active" : "pending"),
        })
      }
    }

    for (var k = 0; k < phases.length; k++) {
      if (k > 0) {
        var conn = el("span", "mini-connector" + (phases[k - 1].status === "complete" ? " done" : ""))
        chain.appendChild(conn)
      }
      var pipClass = "mini-pip"
      if (phases[k].status === "complete") pipClass += " done"
      else if (phases[k].status === "active") pipClass += " active"
      var pip = el("span", pipClass)
      pip.title = phases[k].phase
      chain.appendChild(pip)
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
        // Update border color
        if (task) {
          cards[i].style.borderLeftColor = TASK_STATUS_COLORS[task.status] || "var(--text-dim)"
        }
      } else {
        cards[i].classList.remove("agent-card--selected")
        // Reset border to task's own color
        var otherTask = findTask(cards[i].dataset.taskId)
        if (otherTask) {
          cards[i].style.borderLeftColor = TASK_STATUS_COLORS[otherTask.status] || "var(--text-dim)"
        }
      }
    }

    if (task) {
      renderRightPanel(task)
      connectTerminal(task)
    }

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

    top.appendChild(el("span", "detail-title", (task.project || "\u2014") + " \u00B7 " + task.id))

    var actions = el("div", "detail-actions")
    actions.appendChild(createAgentStatusBadge(task.agentStatus))
    top.appendChild(actions)

    header.appendChild(top)

    // Meta row: description + branch
    var meta = el("div", "detail-meta")
    if (task.description) {
      meta.appendChild(document.createTextNode(task.description))
    }
    if (task.branch) {
      var sep = el("span", null, "\u00B7")
      sep.style.color = "var(--text-dim)"
      meta.appendChild(sep)
      meta.appendChild(el("span", null, task.branch))
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
      // Connector
      if (k > 0) {
        var connClass = "session-chain-connector"
        if (phases[k - 1].status === "done") connClass += " done"
        container.appendChild(el("div", connClass))
      }

      // Pip
      var pip = el("div", "session-chain-pip")

      var dotClass = "session-chain-dot"
      if (phases[k].status === "done") dotClass += " done"
      else if (phases[k].status === "active") dotClass += " active"
      pip.appendChild(el("div", dotClass, phases[k].dotLabel))

      var lblClass = "session-chain-label"
      if (phases[k].status === "active") lblClass += " active"
      var lblText = phases[k].label
      if (phases[k].duration) lblText += " " + phases[k].duration
      pip.appendChild(el("div", lblClass, lblText))

      container.appendChild(pip)
    }
  }

  // ── Preview header ────────────────────────────────────────────────
  function renderPreviewHeader(task) {
    var container = document.getElementById("preview-header")
    if (!container) return

    clearChildren(container)

    var projLabel = el("span", "preview-header-project", task.project || "\u2014")
    container.appendChild(projLabel)

    var agentMeta = AGENT_STATUS_META[task.agentStatus] || AGENT_STATUS_META.idle
    var statusSpan = el("span", "preview-header-status")
    statusSpan.textContent = agentMeta.icon + " " + agentMeta.label
    statusSpan.style.color = agentMeta.color
    container.appendChild(statusSpan)
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

    if (!task.tmuxSession) {
      var placeholder = el("div", "terminal-placeholder", "No session attached.")
      container.appendChild(placeholder)
      return
    }

    // Check if Terminal (xterm.js) is available
    if (typeof Terminal === "undefined") {
      var fallback = el("div", "terminal-placeholder", "Terminal emulator not available. Check xterm.js assets.")
      container.appendChild(fallback)
      return
    }

    var term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "var(--font-mono)",
      theme: {
        background: "#080a0e",
        foreground: "#c8d0dc",
        cursor: "#e8a932",
      },
    })
    var fitAddon = new FitAddon.FitAddon()
    term.loadAddon(fitAddon)
    term.open(container)
    fitAddon.fit()

    state.terminal = term
    state.fitAddon = fitAddon

    var protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
    var wsUrl = protocol + "//" + window.location.host + "/ws/session/" + encodeURIComponent(task.tmuxSession)
    if (state.authToken) wsUrl += "?token=" + encodeURIComponent(state.authToken)
    var ws = new WebSocket(wsUrl)
    state.terminalWs = ws

    ws.binaryType = "arraybuffer"
    ws.onmessage = function (e) {
      if (e.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(e.data))
      } else {
        term.write(e.data)
      }
    }
    ws.onclose = function () { state.terminalWs = null }
    term.onData(function (data) {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    })
  }

  function disconnectTerminal() {
    if (state.terminalWs) {
      state.terminalWs.close()
      state.terminalWs = null
    }
    if (state.terminal) {
      state.terminal.dispose()
      state.terminal = null
    }
    state.fitAddon = null
  }

  // ── Resize handler ────────────────────────────────────────────────
  window.addEventListener("resize", function () {
    if (state.fitAddon) state.fitAddon.fit()
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
    renderChatBar()
    renderAskBanner()
  }

  // ── Chat mode detection ────────────────────────────────────────────
  function detectChatMode() {
    if (state.chatModeOverride) return state.chatModeOverride

    var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null

    if (state.activeView === "agents" && task && task.agentStatus !== "complete" && task.agentStatus !== "idle") {
      return {
        mode: "reply",
        label: "\u21A9 " + task.id + "/" + task.phase,
        icon: "\u21A9",
        color: "var(--accent)",
      }
    }

    var project = ""
    if (task) project = task.project
    else if (state.projectFilter) project = state.projectFilter

    return {
      mode: "new",
      label: project ? "+ " + project : "+ auto-route",
      icon: "+",
      color: "var(--blue)",
    }
  }

  function renderChatBar() {
    var mode = detectChatMode()
    state.chatMode = mode

    var modeBtn = document.getElementById("chat-mode-btn")
    var modeIcon = document.getElementById("chat-mode-icon")
    var modeLabel = document.getElementById("chat-mode-label")
    var input = document.getElementById("chat-input")

    if (modeBtn) modeBtn.style.borderColor = mode.color
    if (modeIcon) { modeIcon.textContent = mode.icon; modeIcon.style.color = mode.color }
    if (modeLabel) modeLabel.textContent = mode.label

    if (mode.mode === "reply") {
      if (input) input.placeholder = "Reply to " + state.selectedTaskId + "..."
    } else {
      if (input) input.placeholder = "Describe a new task..."
    }
  }

  // ── AskUserQuestion banner ─────────────────────────────────────────
  function renderAskBanner() {
    var existing = document.querySelector(".ask-banner")
    if (existing) existing.remove()

    var task = state.selectedTaskId ? findTask(state.selectedTaskId) : null
    if (!task || task.agentStatus !== "waiting" || !task.askQuestion) return

    var banner = el("div", "ask-banner")
    var icon = el("span", "ask-banner-icon", "\u25D0")
    banner.appendChild(icon)
    banner.appendChild(document.createTextNode("Agent is asking: " + task.askQuestion))

    var chatBar = document.getElementById("chat-bar")
    if (chatBar && chatBar.parentNode) {
      chatBar.parentNode.insertBefore(banner, chatBar)
    }

    // Update placeholder to reflect the question
    var input = document.getElementById("chat-input")
    if (input) input.placeholder = "Answer: " + task.askQuestion
  }

  // ── Chat mode override menu ────────────────────────────────────────
  function renderModeMenu() {
    var existing = document.querySelector(".chat-mode-menu")
    if (existing) existing.remove()

    var menu = el("div", "chat-mode-menu open")

    // "New in {project}" options for each project
    for (var i = 0; i < state.projects.length; i++) {
      var proj = state.projects[i]
      var opt = el("button", "chat-mode-option")
      var icon = el("span", "chat-mode-option-icon", "+")
      icon.style.color = "var(--blue)"
      opt.appendChild(icon)
      opt.appendChild(document.createTextNode("New in " + proj.name))
      opt.dataset.mode = "new"
      opt.dataset.project = proj.name
      opt.addEventListener("click", handleModeSelect)
      menu.appendChild(opt)
    }

    // "New (auto-route)"
    var autoOpt = el("button", "chat-mode-option")
    var autoIcon = el("span", "chat-mode-option-icon", "+")
    autoIcon.style.color = "var(--blue)"
    autoOpt.appendChild(autoIcon)
    autoOpt.appendChild(document.createTextNode("New (auto-route)"))
    autoOpt.dataset.mode = "new"
    autoOpt.dataset.project = ""
    autoOpt.addEventListener("click", handleModeSelect)
    menu.appendChild(autoOpt)

    // "Back to auto" if overridden
    if (state.chatModeOverride) {
      var backOpt = el("button", "chat-mode-option")
      var backIcon = el("span", "chat-mode-option-icon", "\u2190")
      backOpt.appendChild(backIcon)
      backOpt.appendChild(document.createTextNode("Back to: auto"))
      backOpt.dataset.mode = "auto"
      backOpt.addEventListener("click", handleModeSelect)
      menu.appendChild(backOpt)
    }

    var chatBar = document.getElementById("chat-bar")
    if (chatBar) {
      chatBar.appendChild(menu)
    }

    // Close on outside click (deferred to avoid immediate close)
    setTimeout(function () {
      document.addEventListener("click", closeModeMenu)
    }, 0)
  }

  function handleModeSelect(e) {
    var btn = e.currentTarget
    if (btn.dataset.mode === "auto") {
      state.chatModeOverride = null
    } else {
      state.chatModeOverride = {
        mode: btn.dataset.mode,
        label: btn.dataset.project ? "+ " + btn.dataset.project : "+ auto-route",
        icon: "+",
        color: "var(--blue)",
        project: btn.dataset.project,
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
    } else {
      openNewTaskModalWithDescription(text)
    }
    input.value = ""
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
      })
      .catch(function (err) {
        console.error("sendTaskInput:", err)
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

    if (tabName === "terminal") {
      if (terminalContainer) terminalContainer.style.display = ""
      if (messagesContainer) messagesContainer.style.display = "none"
      // Re-fit terminal when switching back
      if (state.fitAddon) {
        setTimeout(function () { state.fitAddon.fit() }, 50)
      }
    } else if (tabName === "messages") {
      if (terminalContainer) terminalContainer.style.display = "none"
      if (messagesContainer) messagesContainer.style.display = ""

      // Load messages for the selected task (skip if already loaded for this session)
      if (state.selectedTaskId) {
        var task = findTask(state.selectedTaskId)
        if (task && task.tmuxSession && task.tmuxSession !== state.lastLoadedMessagesSession) {
          loadSessionMessages(task.tmuxSession)
        }
      }
    }
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
    chatBarEl.insertBefore(attachBtn, chatBarEl.querySelector(".chat-input"))
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
    })
  }

  // Escape key
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      if (newTaskModal && newTaskModal.classList.contains("open")) {
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
  renderChatBar()
  fetchTasks()
  fetchProjects()

  // ── ConnectionManager (WebSocket-based event bus) ───────────────
  ;(function initConnectionManager() {
    var wsProto = (location.protocol === "https:") ? "wss:" : "ws:"
    var wsUrl = wsProto + "//" + location.host + "/ws/events"
    var token = state.authToken
    if (token) wsUrl += "?token=" + encodeURIComponent(token)

    var cm = new ConnectionManager(wsUrl)

    // Subscribe to session updates
    cm.subscribe("sessions", function () {
      // fetchMenuData will be added in a future task; fall back to fetchTasks
      if (typeof fetchMenuData === "function") fetchMenuData()
      else fetchTasks()
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
      fetchTasks()
      fetchProjects()
    })

    cm.connect()
  })()
})()
