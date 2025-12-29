import { SyntrixClient } from '@syntrix/client';

// Dynamically use the same host as the page, but connect to port 8080
const API_BASE = `http://${window.location.hostname}:8080`;
const MAX_MESSAGES = 20;

interface ClientState {
    syntrix: SyntrixClient | null;
    connected: boolean;
}

const clients: Record<number, ClientState> = {
    1: { syntrix: null, connected: false },
    2: { syntrix: null, connected: false }
};

// Toggle functions
(window as any).toggleClient = function(panelId: number) {
    const body = document.getElementById(`clientBody${panelId}`)!;
    const toggle = document.getElementById(`toggle${panelId}`)!;
    body.classList.toggle('open');
    toggle.textContent = body.classList.contains('open') ? '▲ Collapse' : '▼ Expand';
};

(window as any).toggleLogs = function() {
    const body = document.getElementById('logsBody')!;
    const toggle = document.getElementById('logsToggle')!;
    body.classList.toggle('open');
    toggle.textContent = body.classList.contains('open') ? '▲ Collapse' : '▼ Expand';
};

function updateStatus(panelId: number, status: string) {
    const dot = document.getElementById(`status${panelId}Dot`)!;
    const text = document.getElementById(`status${panelId}Text`)!;
    dot.className = `status-dot ${status}`;
    const statusText = status === 'connected' ? 'Online' : status === 'connecting' ? 'Connecting...' : 'Offline';
    text.textContent = `Client ${panelId}: ${statusText}`;
}

function log(panelId: number, message: string, type = 'info') {
    const logEl = document.getElementById(`log${panelId}`)!;
    const entry = document.createElement('div');
    entry.className = `log-entry ${type}`;
    const time = new Date().toLocaleTimeString();
    entry.textContent = `[${time}] ${message}`;
    logEl.insertBefore(entry, logEl.firstChild);
    // Keep only last 50 entries
    while (logEl.children.length > 50) {
        logEl.removeChild(logEl.lastChild!);
    }
}

function addToData(panelId: number, delta: any) {
    const dataEl = document.getElementById(`data${panelId}`)!;
    // Remove empty state if exists
    const emptyState = dataEl.querySelector('.empty-state');
    if (emptyState) emptyState.remove();
    
    const item = document.createElement('div');
    item.className = `data-item ${panelId === 2 ? 'client2' : ''}`;
    const doc = delta.document || {};
    const sender = doc.sender || 'unknown';
    const text = doc.text || JSON.stringify(doc);
    const time = new Date().toLocaleTimeString();
    const typeIcon = delta.type === 'create' ? '🆕' : delta.type === 'update' ? '✏️' : delta.type === 'delete' ? '🗑️' : '📸';
    
    item.innerHTML = `<span class="type-icon">${typeIcon}</span><span class="sender">${sender}:</span> <span class="text">${text}</span><span class="time">${time}</span>`;
    dataEl.insertBefore(item, dataEl.firstChild);
    
    while (dataEl.children.length > MAX_MESSAGES) {
        dataEl.removeChild(dataEl.lastChild!);
    }
}

// Helper function to signup or login
async function signupOrLogin(username: string, password: string): Promise<void> {
    // Try signup first, ignore if user already exists
    try {
        await fetch(`${API_BASE}/auth/v1/signup`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, password, tenant: 'default' })
        });
    } catch (e) {
        // Ignore signup errors (user may already exist)
    }
}

// Quick connect both clients
(window as any).quickConnect = async function() {
    const collection = (document.getElementById('collection') as HTMLInputElement).value;
    const btn = document.getElementById('quickConnectBtn') as HTMLButtonElement;
    btn.disabled = true;
    btn.textContent = '⏳ Connecting...';
    
    // Disconnect existing connections first
    for (const panelId of [1, 2]) {
        if (clients[panelId].syntrix) {
            try {
                clients[panelId].syntrix!.realtime().disconnect();
            } catch (e) {
                // Ignore disconnect errors
            }
            clients[panelId].syntrix = null;
            clients[panelId].connected = false;
            updateStatus(panelId, 'disconnected');
        }
        
        // Clear Client Received area
        const dataEl = document.getElementById(`data${panelId}`)!;
        dataEl.innerHTML = '<div class="empty-state">No messages yet</div>';
        
        // Clear log area
        const logEl = document.getElementById(`log${panelId}`)!;
        logEl.innerHTML = '';
    }
    
    try {
        for (const panelId of [1, 2]) {
            const username = (document.getElementById(`username${panelId}`) as HTMLInputElement).value;
            const password = 'password_' + username; // Auto-generate password (min 8 chars)
            
            // Ensure user exists (signup if needed)
            await signupOrLogin(username, password);
            
            const syntrix = new SyntrixClient(API_BASE, { tenantId: 'default' });
            await syntrix.login(username, password);
            clients[panelId].syntrix = syntrix;
            log(panelId, `✅ Logged in as ${username}`, 'event');
            
            const rt = syntrix.realtime();
            
            rt.on('onConnect', () => {
                updateStatus(panelId, 'connected');
                clients[panelId].connected = true;
                log(panelId, '✅ Connected', 'event');
                
                // Auto subscribe
                rt.subscribe({
                    query: { collection, filters: [] },
                    includeData: true,
                    sendSnapshot: true
                });
                log(panelId, `📡 Subscribed to "${collection}"`, 'event');
            });

            rt.on('onDisconnect', () => {
                updateStatus(panelId, 'disconnected');
                clients[panelId].connected = false;
                log(panelId, '🔌 Disconnected', 'info');
            });

            rt.on('onError', (error) => {
                log(panelId, `❌ Error: ${error.message}`, 'error');
            });

            rt.on('onEvent', (event) => {
                log(panelId, `📥 ${event.delta.type}: ${event.delta.document?.text || event.delta.id}`, 'event');
                addToData(panelId, event.delta);
            });

            rt.on('onSnapshot', (snapshot) => {
                log(panelId, `📸 Snapshot: ${snapshot.documents.length} docs`, 'event');
                snapshot.documents.forEach(doc => {
                    addToData(panelId, { type: 'snapshot', document: doc });
                });
            });

            await rt.connect();
        }
    } catch (e: any) {
        log(1, `❌ Quick connect failed: ${e.message}`, 'error');
    }
    
    btn.disabled = false;
    btn.textContent = '⚡ Quick Connect Both';
};

(window as any).disconnectAll = function() {
    for (const panelId of [1, 2]) {
        if (clients[panelId].syntrix) {
            clients[panelId].syntrix!.realtime().disconnect();
            clients[panelId].syntrix = null;
            clients[panelId].connected = false;
            updateStatus(panelId, 'disconnected');
        }
    }
};

(window as any).login = async function(panelId: number) {
    const username = (document.getElementById(`username${panelId}`) as HTMLInputElement).value;
    const password = 'password_' + username;
    
    try {
        log(panelId, `Logging in as ${username}...`, 'info');
        
        // Ensure user exists (signup if needed)
        await signupOrLogin(username, password);
        
        const syntrix = new SyntrixClient(API_BASE, { tenantId: 'default' });
        await syntrix.login(username, password);
        clients[panelId].syntrix = syntrix;
        log(panelId, `✅ Logged in`, 'event');
        
        (document.getElementById(`connectBtn${panelId}`) as HTMLButtonElement).disabled = false;
    } catch (e: any) {
        log(panelId, `❌ Login failed: ${e.message}`, 'error');
    }
};

(window as any).connectRealtime = async function(panelId: number) {
    const syntrix = clients[panelId].syntrix;
    if (!syntrix) return;

    updateStatus(panelId, 'connecting');
    const collection = (document.getElementById('collection') as HTMLInputElement).value;

    try {
        const rt = syntrix.realtime();
        
        rt.on('onConnect', () => {
            updateStatus(panelId, 'connected');
            clients[panelId].connected = true;
            log(panelId, '✅ Connected', 'event');
            (document.getElementById(`subBtn${panelId}`) as HTMLButtonElement).disabled = false;
            (document.getElementById(`disconnectBtn${panelId}`) as HTMLButtonElement).disabled = false;
        });

        rt.on('onDisconnect', () => {
            updateStatus(panelId, 'disconnected');
            clients[panelId].connected = false;
            log(panelId, '🔌 Disconnected', 'info');
        });

        rt.on('onError', (error) => {
            log(panelId, `❌ ${error.message}`, 'error');
        });

        rt.on('onEvent', (event) => {
            log(panelId, `📥 ${event.delta.type}`, 'event');
            addToData(panelId, event.delta);
        });

        rt.on('onSnapshot', (snapshot) => {
            log(panelId, `📸 ${snapshot.documents.length} docs`, 'event');
            snapshot.documents.forEach(doc => {
                addToData(panelId, { type: 'snapshot', document: doc });
            });
        });

        await rt.connect();
    } catch (e: any) {
        log(panelId, `❌ Connect failed: ${e.message}`, 'error');
        updateStatus(panelId, 'disconnected');
    }
};

(window as any).disconnectRealtime = function(panelId: number) {
    if (clients[panelId].syntrix) {
        clients[panelId].syntrix!.realtime().disconnect();
    }
};

(window as any).subscribe = function(panelId: number) {
    const syntrix = clients[panelId].syntrix;
    if (!syntrix) return;

    const collection = (document.getElementById('collection') as HTMLInputElement).value;
    syntrix.realtime().subscribe({
        query: { collection, filters: [] },
        includeData: true,
        sendSnapshot: true
    });
    log(panelId, `📡 Subscribed to "${collection}"`, 'event');
};

(window as any).sendMessage = async function() {
    const panelId = parseInt((document.getElementById('sendAs') as HTMLSelectElement).value);
    const syntrix = clients[panelId].syntrix;
    
    if (!syntrix) {
        alert('Please connect Client ' + panelId + ' first!');
        return;
    }

    const text = (document.getElementById('messageText') as HTMLInputElement).value;
    const sender = (document.getElementById(`username${panelId}`) as HTMLInputElement).value;
    const collection = (document.getElementById('collection') as HTMLInputElement).value;

    try {
        log(panelId, `📤 Sending: "${text}"`, 'send');
        await syntrix.collection(collection).add({ text, sender });
        log(panelId, `✅ Sent`, 'event');
        (document.getElementById('messageText') as HTMLInputElement).value = '';
    } catch (e: any) {
        log(panelId, `❌ Send failed: ${e.message}`, 'error');
    }
};

// Enter key to send
document.getElementById('messageText')?.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') (window as any).sendMessage();
});
