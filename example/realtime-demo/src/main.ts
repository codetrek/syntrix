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

function updateStatus(panelId: number, status: string) {
    // Update top status bar
    const dot = document.getElementById(`status${panelId}Dot`)!;
    const text = document.getElementById(`status${panelId}Text`)!;
    dot.className = `status-dot ${status}`;
    const statusText = status === 'connected' ? 'Online' : status === 'connecting' ? 'Connecting...' : 'Offline';
    text.textContent = `Client ${panelId}: ${statusText}`;
    
    // Update panel status indicator
    const panelDot = document.getElementById(`panel${panelId}StatusDot`);
    if (panelDot) {
        panelDot.className = `status-dot ${status}`;
    }
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

// Track if both clients are connected
let bothConnected = false;

// Helper to update client settings button states
function updateClientButtons(panelId: number, connected: boolean) {
    const connectBtn = document.getElementById(`connectBtn${panelId}`) as HTMLButtonElement;
    const disconnectBtn = document.getElementById(`disconnectBtn${panelId}`) as HTMLButtonElement;
    connectBtn.disabled = connected;
    disconnectBtn.disabled = !connected;
}

// Helper to check and update bothConnected state
function updateBothConnectedState() {
    bothConnected = clients[1].connected && clients[2].connected;
    updateQuickConnectButton();
}

// Helper to update button state
function updateQuickConnectButton() {
    const btn = document.getElementById('quickConnectBtn') as HTMLButtonElement;
    if (bothConnected) {
        btn.textContent = '🔌 Disconnect Both';
        btn.classList.add('danger');
        btn.classList.remove('success');
    } else {
        btn.textContent = '⚡ Quick Connect Both';
        btn.classList.remove('danger');
        btn.classList.add('success');
    }
}

// Quick connect both clients
(window as any).quickConnect = async function() {
    const btn = document.getElementById('quickConnectBtn') as HTMLButtonElement;
    
    // If already connected, disconnect instead
    if (bothConnected) {
        (window as any).disconnectAll();
        return;
    }
    
    const collection = (document.getElementById('collection') as HTMLInputElement).value;
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
                console.log(`[Client ${panelId}] WebSocket connected at ${new Date().toISOString()}`);
                updateClientButtons(panelId, true);
                
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
                const lastMsgTime = rt.getLastMessageTime();
                const timeSinceLastMsg = lastMsgTime ? Date.now() - lastMsgTime : 'N/A';
                log(panelId, `🔌 Disconnected (last msg: ${timeSinceLastMsg}ms ago)`, 'info');
                console.log(`[Client ${panelId}] WebSocket disconnected at ${new Date().toISOString()}, last message was ${timeSinceLastMsg}ms ago`);
                updateClientButtons(panelId, false);
                updateBothConnectedState();
            });

            rt.on('onStateChange', (state) => {
                console.log(`[Client ${panelId}] State changed to: ${state} at ${new Date().toISOString()}`);
                if (state === 'connecting') {
                    log(panelId, '🔄 Reconnecting...', 'info');
                }
            });

            rt.on('onError', (error) => {
                log(panelId, `❌ Error: ${error.message}`, 'error');
                console.error(`[Client ${panelId}] WebSocket error at ${new Date().toISOString()}:`, error);
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
        // Mark both as connected
        bothConnected = true;
    } catch (e: any) {
        log(1, `❌ Quick connect failed: ${e.message}`, 'error');
        bothConnected = false;
    }
    
    btn.disabled = false;
    updateQuickConnectButton();
};

(window as any).disconnectAll = function() {
    const btn = document.getElementById('quickConnectBtn') as HTMLButtonElement;
    btn.disabled = true;
    btn.textContent = '⏳ Disconnecting...';
    
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
            updateClientButtons(panelId, false);
        }
        
        // Clear Client Received area
        const dataEl = document.getElementById(`data${panelId}`)!;
        dataEl.innerHTML = '<div class="empty-state">No messages yet</div>';
        
        // Clear log area
        const logEl = document.getElementById(`log${panelId}`)!;
        logEl.innerHTML = '';
    }
    
    bothConnected = false;
    btn.disabled = false;
    updateQuickConnectButton();
};

(window as any).connectRealtime = async function(panelId: number) {
    const username = (document.getElementById(`username${panelId}`) as HTMLInputElement).value;
    const password = 'password_' + username;
    const collection = (document.getElementById('collection') as HTMLInputElement).value;
    const connectBtn = document.getElementById(`connectBtn${panelId}`) as HTMLButtonElement;
    const disconnectBtn = document.getElementById(`disconnectBtn${panelId}`) as HTMLButtonElement;
    
    connectBtn.disabled = true;
    updateStatus(panelId, 'connecting');
    
    try {
        // Ensure user exists (signup if needed) and login
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
            console.log(`[Client ${panelId}] WebSocket connected at ${new Date().toISOString()}`);
            disconnectBtn.disabled = false;
            updateBothConnectedState();
            
            // Auto subscribe after connect
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
            const lastMsgTime = rt.getLastMessageTime();
            const timeSinceLastMsg = lastMsgTime ? Date.now() - lastMsgTime : 'N/A';
            log(panelId, `🔌 Disconnected (last msg: ${timeSinceLastMsg}ms ago)`, 'info');
            console.log(`[Client ${panelId}] WebSocket disconnected at ${new Date().toISOString()}, last message was ${timeSinceLastMsg}ms ago`);
            connectBtn.disabled = false;
            disconnectBtn.disabled = true;
            updateBothConnectedState();
        });

        rt.on('onStateChange', (state) => {
            console.log(`[Client ${panelId}] State changed to: ${state} at ${new Date().toISOString()}`);
            if (state === 'connecting') {
                log(panelId, '🔄 Reconnecting...', 'info');
            }
        });

        rt.on('onError', (error) => {
            log(panelId, `❌ ${error.message}`, 'error');
            console.error(`[Client ${panelId}] WebSocket error at ${new Date().toISOString()}:`, error);
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
        connectBtn.disabled = false;
    }
};

(window as any).disconnectRealtime = function(panelId: number) {
    if (clients[panelId].syntrix) {
        try {
            clients[panelId].syntrix!.realtime().disconnect();
        } catch (e) {
            // Ignore disconnect errors
        }
        clients[panelId].syntrix = null;
        clients[panelId].connected = false;
        updateStatus(panelId, 'disconnected');
        
        // Reset button states
        updateClientButtons(panelId, false);
        updateBothConnectedState();
        
        // Clear Client Received area
        const dataEl = document.getElementById(`data${panelId}`)!;
        dataEl.innerHTML = '<div class="empty-state">No messages yet</div>';
        
        // Clear log area
        const logEl = document.getElementById(`log${panelId}`)!;
        logEl.innerHTML = '';
    }
};

// subscribe function is no longer needed as auto-subscribe happens on connect

(window as any).sendMessage = async function(panelId: number) {
    const syntrix = clients[panelId].syntrix;
    
    if (!syntrix) {
        alert('Please connect Client ' + panelId + ' first!');
        return;
    }

    const text = (document.getElementById(`messageText${panelId}`) as HTMLInputElement).value;
    const sender = (document.getElementById(`username${panelId}`) as HTMLInputElement).value;
    const collection = (document.getElementById('collection') as HTMLInputElement).value;

    try {
        log(panelId, `📤 Sending: "${text}"`, 'send');
        await syntrix.collection(collection).add({ text, sender });
        log(panelId, `✅ Sent`, 'event');
        (document.getElementById(`messageText${panelId}`) as HTMLInputElement).value = '';
    } catch (e: any) {
        log(panelId, `❌ Send failed: ${e.message}`, 'error');
    }
};

// Enter key to send for both clients
document.getElementById('messageText1')?.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') (window as any).sendMessage(1);
});
document.getElementById('messageText2')?.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') (window as any).sendMessage(2);
});

// Server health check
let serverOnline = false;

function updateServerStatus(online: boolean) {
    serverOnline = online;
    const dot = document.getElementById('serverStatusDot')!;
    const text = document.getElementById('serverStatusText')!;
    
    if (online) {
        dot.className = 'status-dot connected';
        text.textContent = 'Server: Online';
    } else {
        dot.className = 'status-dot disconnected';
        text.textContent = 'Server: Offline';
    }
}

async function checkServerHealth() {
    try {
        // Use a simple fetch with timeout promise race
        const fetchPromise = fetch(`${API_BASE}/health`, {
            method: 'GET',
            mode: 'cors'
        });
        
        const timeoutPromise = new Promise<Response>((_, reject) => {
            setTimeout(() => reject(new Error('timeout')), 5000);
        });
        
        const response = await Promise.race([fetchPromise, timeoutPromise]);
        updateServerStatus(response.ok);
    } catch (e) {
        // Log error for debugging
        console.log('[Health Check] Failed:', e);
        updateServerStatus(false);
    }
}

// Check server health on load and periodically
checkServerHealth();
setInterval(checkServerHealth, 10000); // Check every 10 seconds

