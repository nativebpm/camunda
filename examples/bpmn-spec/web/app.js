let bpmnViewer = null;
let processes = [];
let currentProcess = null;
let activeInstance = null;
let activeActivities = [];
let pollIntervalId = null;

// Initialize bpmn.js Viewer on DOM load
document.addEventListener('DOMContentLoaded', () => {
  bpmnViewer = new BpmnJS({
    container: '#canvas',
    keyboard: {
      bindTo: window
    }
  });

  // Setup Zoom button handlers
  document.getElementById('btn-zoom-in').addEventListener('click', () => bpmnViewer.get('canvas').zoom(1.2));
  document.getElementById('btn-zoom-out').addEventListener('click', () => bpmnViewer.get('canvas').zoom(0.8));
  document.getElementById('btn-zoom-fit').addEventListener('click', () => bpmnViewer.get('canvas').zoom('fit-viewport'));

  // Load process definitions from backend
  loadProcesses();

  // Handle process start modal trigger
  document.getElementById('btn-start-process').addEventListener('click', showStartModal);
  document.getElementById('btn-modal-cancel').addEventListener('click', hideStartModal);
  document.getElementById('btn-modal-start').addEventListener('click', startProcessInstance);

  // Close instance interaction panel
  document.getElementById('btn-close-instance').addEventListener('click', () => {
    stopPolling();
    activeInstance = null;
    document.getElementById('interaction-panel').classList.add('hidden');
    clearActiveHighlights();
  });

  // Send custom event correlation
  document.getElementById('btn-send-custom-event').addEventListener('click', () => {
    const eventName = document.getElementById('custom-event-name').value.trim();
    if (eventName) {
      triggerEvent(eventName);
      document.getElementById('custom-event-name').value = '';
    }
  });
});

// Fetch processes metadata from server
async function loadProcesses() {
  try {
    const res = await fetch('/api/processes');
    processes = await res.json();
    
    const listContainer = document.getElementById('process-list');
    listContainer.innerHTML = '';
    
    processes.forEach(proc => {
      const item = document.createElement('div');
      item.className = 'p-3 bg-slate-800/50 hover:bg-slate-800 border border-slate-700/50 rounded-lg cursor-pointer transition flex flex-col space-y-1 hover:border-emerald-500/50';
      item.innerHTML = `
        <h3 class="font-outfit text-sm font-semibold text-slate-200">${proc.name}</h3>
        <p class="text-xs text-slate-400 leading-relaxed">${proc.description}</p>
      `;
      item.addEventListener('click', () => selectProcess(proc));
      listContainer.appendChild(item);
    });

    // Auto-select first process
    if (processes.length > 0) {
      selectProcess(processes[0]);
    }
  } catch (err) {
    console.error('Failed to load processes:', err);
    document.getElementById('process-list').innerHTML = `
      <p class="text-xs text-red-400 p-4">Failed to connect to backend server. Make sure it is running on port 8081.</p>
    `;
  }
}

// Select and render process diagram
async function selectProcess(proc) {
  currentProcess = proc;
  stopPolling();
  activeInstance = null;
  clearActiveHighlights();
  document.getElementById('interaction-panel').classList.add('hidden');

  // Highlight selected item in sidebar
  const items = document.querySelectorAll('#process-list > div');
  processes.forEach((p, idx) => {
    if (p.key === proc.key) {
      items[idx].classList.add('border-emerald-500', 'bg-slate-800');
      items[idx].classList.remove('border-slate-700/50', 'bg-slate-800/50');
    } else {
      items[idx].classList.add('border-slate-700/50', 'bg-slate-800/50');
      items[idx].classList.remove('border-emerald-500', 'bg-slate-800');
    }
  });

  // Set description banners
  document.getElementById('current-process-name').innerText = proc.name;
  document.getElementById('current-process-desc').innerText = proc.description;

  // Show loading indicator
  showLoading(true);
  try {
    const res = await fetch(`/api/processes/${proc.key}/xml`);
    const xml = await res.text();
    
    await bpmnViewer.importXML(xml);
    bpmnViewer.get('canvas').zoom('fit-viewport');
  } catch (err) {
    console.error('Failed to import XML:', err);
  } finally {
    showLoading(false);
  }
}

// Show modal configuring start variables dynamically based on selected process
function showStartModal() {
  if (!currentProcess) return;
  
  document.getElementById('modal-process-title').innerText = `Start: ${currentProcess.name}`;
  const fieldsContainer = document.getElementById('modal-fields');
  fieldsContainer.innerHTML = '';

  if (currentProcess.key === 'gateways_process') {
    fieldsContainer.innerHTML = `
      <div>
        <label class="block text-xs font-medium text-slate-400 mb-1">Input Score (Integer)</label>
        <input type="number" id="start-var-score" value="65" class="w-full px-3 py-2 bg-slate-950 border border-slate-800 rounded text-slate-200 focus:outline-none focus:border-slate-700 text-sm">
        <p class="text-[10px] text-slate-500 mt-1">Score > 50 routes to High Score task, otherwise Low Score task.</p>
      </div>
    `;
  } else if (currentProcess.key === 'events_process') {
    fieldsContainer.innerHTML = `
      <div class="flex items-center space-x-3 bg-slate-950/40 p-3 border border-slate-800 rounded-lg">
        <input type="checkbox" id="start-var-fail-payment" class="w-4 h-4 text-emerald-500 bg-slate-950 border-slate-800 rounded focus:ring-emerald-500 focus:ring-offset-slate-900 focus:ring-2">
        <div>
          <label for="start-var-fail-payment" class="block text-xs font-semibold text-slate-300">Simulate Payment Failure</label>
          <p class="text-[10px] text-slate-500">If checked, the payment task throws a BPMN Error which is caught by the Boundary Error event.</p>
        </div>
      </div>
    `;
  } else if (currentProcess.key === 'subprocesses_process') {
    fieldsContainer.innerHTML = `
      <div class="flex items-center space-x-3 bg-slate-950/40 p-3 border border-slate-800 rounded-lg">
        <input type="checkbox" id="start-var-fail-shipping" class="w-4 h-4 text-emerald-500 bg-slate-950 border-slate-800 rounded focus:ring-emerald-500">
        <div>
          <label for="start-var-fail-shipping" class="block text-xs font-semibold text-slate-300">Simulate Shipping Failure</label>
          <p class="text-[10px] text-slate-500">If checked, the 'Ship Items' task in the Embedded Subprocess will throw a SHIPPING_FAILED error.</p>
        </div>
      </div>
    `;
  } else if (currentProcess.key === 'dmn_process') {
    fieldsContainer.innerHTML = `
      <div>
        <label class="block text-xs font-medium text-slate-400 mb-1">Membership Level</label>
        <select id="start-var-membership" class="w-full px-3 py-2 bg-slate-950 border border-slate-800 rounded text-slate-200 focus:outline-none focus:border-slate-700 text-sm">
          <option value="gold">Gold (20% Discount)</option>
          <option value="silver">Silver (10% Discount)</option>
          <option value="bronze">Bronze (5% Discount)</option>
          <option value="none">None (0% Discount)</option>
        </select>
      </div>
    `;
  }

  document.getElementById('start-modal').classList.remove('hidden');
}

function hideStartModal() {
  document.getElementById('start-modal').classList.add('hidden');
}

// Start instance by calling backend
async function startProcessInstance() {
  if (!currentProcess) return;

  const variables = {};
  if (currentProcess.key === 'gateways_process') {
    variables.inputScore = parseInt(document.getElementById('start-var-score').value) || 60;
  } else if (currentProcess.key === 'events_process') {
    variables.failPayment = document.getElementById('start-var-fail-payment').checked;
  } else if (currentProcess.key === 'subprocesses_process') {
    variables.failShipping = document.getElementById('start-var-fail-shipping').checked;
  } else if (currentProcess.key === 'dmn_process') {
    variables.membership = document.getElementById('start-var-membership').value;
  }

  hideStartModal();
  showLoading(true);

  try {
    const res = await fetch(`/api/processes/${currentProcess.key}/start`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(variables)
    });
    
    if (!res.ok) {
      const errMsg = await res.text();
      throw new Error(errMsg);
    }

    activeInstance = await res.json();
    
    // Open Side Panel
    document.getElementById('inst-id-span').innerText = activeInstance.processInstanceId;
    document.getElementById('inst-bk-span').innerText = activeInstance.businessKey;
    document.getElementById('interaction-panel').classList.remove('hidden');

    // Setup predefined events for correlation
    setupPredefinedEvents();

    // Start auto polling for state changes
    startPolling(activeInstance.processInstanceId);

  } catch (err) {
    alert('Failed to start process: ' + err.message);
  } finally {
    showLoading(false);
  }
}

// Setup correlation buttons for process
function setupPredefinedEvents() {
  const container = document.getElementById('predefined-events');
  container.innerHTML = '';
  
  if (currentProcess.key === 'events_process') {
    container.innerHTML = `
      <button onclick="triggerEvent('cancelOrder')" class="px-2 py-1 bg-red-950/60 hover:bg-red-900 border border-red-800 text-red-300 rounded text-xs transition">
        Msg: cancelOrder
      </button>
    `;
  } else if (currentProcess.key === 'subprocesses_process') {
    container.innerHTML = `
      <button onclick="triggerEvent('cancelFulfillment')" class="px-2 py-1 bg-red-950/60 hover:bg-red-900 border border-red-800 text-red-300 rounded text-xs transition">
        Msg: cancelFulfillment
      </button>
    `;
  } else {
    container.innerHTML = `<span class="text-xs text-slate-500 italic">No predefined events for this flow</span>`;
  }
}

// Trigger event correlation
async function triggerEvent(eventName) {
  if (!activeInstance) return;
  showLoading(true);
  try {
    const res = await fetch(`/api/instances/${activeInstance.processInstanceId}/trigger-event`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ eventName })
    });
    
    if (!res.ok) {
      const err = await res.text();
      throw new Error(err);
    }
    
    // Force poll update
    pollState();
  } catch (err) {
    alert('Event correlation failed: ' + err.message);
  } finally {
    showLoading(false);
  }
}

// Polling management
function startPolling(instanceId) {
  stopPolling();
  // Poll immediately and set interval
  pollState();
  pollIntervalId = setInterval(pollState, 1500);
}

function stopPolling() {
  if (pollIntervalId) {
    clearInterval(pollIntervalId);
    pollIntervalId = null;
  }
}

async function pollState() {
  if (!activeInstance) return;
  
  const id = activeInstance.processInstanceId;
  try {
    // 1. Fetch active steps (nodes)
    const activeRes = await fetch(`/api/instances/${id}/active-activities`);
    if (activeRes.status === 404) {
      // Process finished
      stopPolling();
      clearActiveHighlights();
      document.getElementById('user-tasks-list').innerHTML = `
        <p class="text-xs text-emerald-400 font-semibold italic">Process instance finished successfully!</p>
      `;
      document.getElementById('variables-table-body').innerHTML = `
        <tr><td colspan="3" class="text-slate-500 py-2 italic text-center">Process finished</td></tr>
      `;
      return;
    }
    
    if (!activeRes.ok) return;
    const newActive = await resToJSON(activeRes);
    
    // Highlight elements
    updateDiagramHighlights(newActive);

    // 2. Fetch User Tasks
    const taskRes = await fetch(`/api/instances/${id}/tasks`);
    if (taskRes.ok) {
      const tasks = await resToJSON(taskRes);
      renderUserTasks(tasks);
    }

    // 3. Fetch Variables
    const varRes = await fetch(`/api/instances/${id}/variables`);
    if (varRes.ok) {
      const variables = await resToJSON(varRes);
      renderVariables(variables);
    }

  } catch (err) {
    console.error('Polling error:', err);
  }
}

// Helper to decode safely
function resToJSON(res) {
  return res.json().catch(() => ({}));
}

// Highlight active elements on diagram
function updateDiagramHighlights(newActive) {
  const canvas = bpmnViewer.get('canvas');
  
  // Clear old markers not in newActive
  activeActivities.forEach(actID => {
    if (!newActive.includes(actID)) {
      try { canvas.removeMarker(actID, 'highlight-active-node'); } catch(e) {}
    }
  });

  // Add new markers
  newActive.forEach(actID => {
    try { canvas.addMarker(actID, 'highlight-active-node'); } catch(e) {}
  });

  activeActivities = newActive;
}

function clearActiveHighlights() {
  const canvas = bpmnViewer.get('canvas');
  activeActivities.forEach(actID => {
    try { canvas.removeMarker(actID, 'highlight-active-node'); } catch(e) {}
  });
  activeActivities = [];
}

// Render active User Tasks list with interactive submit forms
function renderUserTasks(tasks) {
  const container = document.getElementById('user-tasks-list');
  container.innerHTML = '';
  
  if (!tasks || tasks.length === 0) {
    container.innerHTML = `<p class="text-xs text-slate-500 italic">No active User Tasks. Wait for workers or trigger events.</p>`;
    return;
  }

  tasks.forEach(task => {
    const card = document.createElement('div');
    card.className = 'bg-slate-950/50 border border-slate-800 rounded-lg p-3 space-y-2';
    
    let formHTML = '';
    // Custom form for Gateways process User Task
    if (task.taskDefinitionKey === 'Activity_User_Approve') {
      formHTML = `
        <div class="flex items-center space-x-2 py-1">
          <input type="checkbox" id="user-var-approve" class="w-4 h-4 text-emerald-500 bg-slate-950 border-slate-800 rounded">
          <label for="user-var-approve" class="text-xs text-slate-300">Approve process Flow</label>
        </div>
      `;
    }

    card.innerHTML = `
      <div class="flex justify-between items-center">
        <span class="text-xs font-semibold text-slate-200">${task.name}</span>
        <span class="text-[9px] font-mono text-slate-500">ID: ${task.id.slice(0,6)}</span>
      </div>
      ${formHTML}
      <button onclick="completeUserTask('${task.id}', '${task.taskDefinitionKey}')" class="w-full py-1.5 bg-emerald-500 hover:bg-emerald-400 text-slate-950 font-bold rounded text-xs shadow-lg shadow-emerald-500/10 transition">
        Complete Task
      </button>
    `;
    container.appendChild(card);
  });
}

// Send user task completion to backend
async function completeUserTask(taskId, taskDefKey) {
  const variables = {};
  if (taskDefKey === 'Activity_User_Approve') {
    variables.approved = document.getElementById('user-var-approve').checked;
  }

  showLoading(true);
  try {
    const res = await fetch(`/api/tasks/${taskId}/complete`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(variables)
    });

    if (!res.ok) {
      const err = await res.text();
      throw new Error(err);
    }
    
    // Force poll update
    pollState();
  } catch (err) {
    alert('Failed to complete task: ' + err.message);
  } finally {
    showLoading(false);
  }
}

// Render variables list
function renderVariables(variables) {
  const tbody = document.getElementById('variables-table-body');
  tbody.innerHTML = '';
  
  const keys = Object.keys(variables);
  if (keys.length === 0) {
    tbody.innerHTML = `<tr><td colspan="3" class="text-slate-500 py-2 italic text-center">No variables present</td></tr>`;
    return;
  }

  keys.forEach(k => {
    const v = variables[k];
    const tr = document.createElement('tr');
    tr.className = 'border-b border-slate-800/40 hover:bg-slate-950/20';
    tr.innerHTML = `
      <td class="py-1.5 pr-2 text-slate-300 font-medium">${k}</td>
      <td class="py-1.5 pr-2 text-slate-500 text-[10px] uppercase">${v.type}</td>
      <td class="py-1.5 text-emerald-400 truncate max-w-[120px]" title="${v.value}">${v.value}</td>
    `;
    tbody.appendChild(tr);
  });
}

function showLoading(show) {
  const el = document.getElementById('loading-overlay');
  if (show) {
    el.classList.remove('hidden');
  } else {
    el.classList.add('hidden');
  }
}
