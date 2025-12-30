import { describe, it, expect, mock, beforeEach, afterEach } from 'bun:test';
import { RealtimeClient, RealtimeClientOptions } from './realtime';

// Mock WebSocket for testing
class MockWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror: ((event: any) => void) | null = null;

  sentMessages: string[] = [];

  constructor(public url: string) {
    // Simulate async connection
    setTimeout(() => {
      this.readyState = MockWebSocket.OPEN;
      this.onopen?.();
    }, 0);
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
  }

  // Helper to simulate receiving a message
  simulateMessage(data: any) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }
}

describe('RealtimeClient Keepalive', () => {
  let originalWebSocket: typeof globalThis.WebSocket;

  beforeEach(() => {
    // Save original WebSocket
    originalWebSocket = globalThis.WebSocket;
    // @ts-ignore - Mock WebSocket
    globalThis.WebSocket = MockWebSocket;
  });

  afterEach(() => {
    // Restore original WebSocket
    globalThis.WebSocket = originalWebSocket;
  });

  it('should initialize with default options', () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    expect(client.getState()).toBe('disconnected');
    expect(client.getLastMessageTime()).toBe(0);
  });

  it('should accept custom options', () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const options: RealtimeClientOptions = {
      maxReconnectAttempts: 10,
      reconnectDelayMs: 2000,
      activityTimeoutMs: 60000,
    };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any, options);

    // Options are private, so we verify behavior indirectly
    expect(client.getState()).toBe('disconnected');
  });

  it('should update lastMessageTime on connect', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    const beforeConnect = Date.now();
    await client.connect();
    const afterConnect = Date.now();

    expect(client.getLastMessageTime()).toBeGreaterThanOrEqual(beforeConnect);
    expect(client.getLastMessageTime()).toBeLessThanOrEqual(afterConnect);

    client.disconnect();
  });

  it('should update lastMessageTime on message received', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    await client.connect();
    const initialTime = client.getLastMessageTime();

    // Wait a bit to ensure time difference
    await new Promise((resolve) => setTimeout(resolve, 10));

    // Get the internal WebSocket and simulate a message
    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;
    ws.simulateMessage({ type: 'auth_ack' });

    expect(client.getLastMessageTime()).toBeGreaterThan(initialTime);

    client.disconnect();
  });

  it('should send auth message with token on connect', async () => {
    const tokenProvider = { getToken: async () => 'auth-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    await client.connect();

    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;
    const payloads = ws.sentMessages.map((m) => JSON.parse(m));

    const authMsg = payloads.find((m) => m.type === 'auth');
    expect(authMsg).toBeDefined();
    expect(authMsg.payload.token).toBe('auth-token');

    client.disconnect();
  });

  it('should refresh token and retry auth on unauthorized error once', async () => {
    const tokenProvider = {
      getToken: mock(async () => 'stale-token'),
      refreshToken: mock(async () => 'fresh-token'),
    } as any;
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    await client.connect();

    // @ts-ignore
    const ws = client['ws'] as MockWebSocket;
    ws.sentMessages = []; // reset messages to inspect retry

    // Simulate server auth error
    ws.simulateMessage({ type: 'error', payload: { message: 'unauthorized' } });

    // Allow async refresh to run
    await new Promise((resolve) => setTimeout(resolve, 10));

    const payloads = ws.sentMessages.map((m) => JSON.parse(m));
    const retry = payloads.find((m) => m.type === 'auth' && m.id === 'auth-retry');

    expect(tokenProvider.refreshToken).toHaveBeenCalledTimes(1);
    expect(retry).toBeDefined();
    expect(retry.payload.token).toBe('fresh-token');

    // Subsequent unauthorized should not trigger another refresh
    ws.sentMessages = [];
    ws.simulateMessage({ type: 'error', payload: { message: 'unauthorized again' } });
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(ws.sentMessages.length).toBe(0);

    client.disconnect();
  });

  it('should trigger reconnect on activity timeout', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    // Very short timeout for testing
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any, {
      activityTimeoutMs: 50,
    });

    let disconnectCount = 0;
    client.on('onDisconnect', () => {
      disconnectCount++;
    });

    await client.connect();

    // Wait for activity timeout to trigger
    await new Promise((resolve) => setTimeout(resolve, 100));

    // Should have triggered disconnect due to inactivity
    expect(disconnectCount).toBeGreaterThanOrEqual(1);
  });

  it('should not timeout if messages are received', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any, {
      activityTimeoutMs: 100,
    });

    let disconnectCount = 0;
    client.on('onDisconnect', () => {
      disconnectCount++;
    });

    await client.connect();

    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;

    // Send messages periodically to keep connection alive
    const interval = setInterval(() => {
      if (ws.readyState === MockWebSocket.OPEN) {
        ws.simulateMessage({ type: 'event', payload: {} });
      }
    }, 30);

    // Wait longer than timeout
    await new Promise((resolve) => setTimeout(resolve, 150));

    clearInterval(interval);

    // Should NOT have disconnected because we kept receiving messages
    expect(disconnectCount).toBe(0);

    client.disconnect();
  });

  it('should stop activity check on disconnect', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any, {
      activityTimeoutMs: 50,
    });

    await client.connect();

    // @ts-ignore - access private timer for testing
    expect(client['activityCheckTimer']).not.toBeNull();

    client.disconnect();

    // @ts-ignore - access private timer for testing
    expect(client['activityCheckTimer']).toBeNull();
  });

  it('should update lastMessageTime when receiving heartbeat message', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    await client.connect();
    const initialTime = client.getLastMessageTime();

    // Wait a bit to ensure time difference
    await new Promise((resolve) => setTimeout(resolve, 10));

    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;
    
    // Simulate receiving a heartbeat message from server
    ws.simulateMessage({ type: 'heartbeat' });

    expect(client.getLastMessageTime()).toBeGreaterThan(initialTime);

    client.disconnect();
  });

  it('should keep connection alive when receiving periodic heartbeat messages', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any, {
      activityTimeoutMs: 100,
    });

    let disconnectCount = 0;
    client.on('onDisconnect', () => {
      disconnectCount++;
    });

    await client.connect();

    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;

    // Simulate server sending heartbeat messages periodically (like server's 30s interval)
    const interval = setInterval(() => {
      if (ws.readyState === MockWebSocket.OPEN) {
        ws.simulateMessage({ type: 'heartbeat' });
      }
    }, 30);

    // Wait longer than timeout
    await new Promise((resolve) => setTimeout(resolve, 150));

    clearInterval(interval);

    // Should NOT have disconnected because heartbeats kept the connection alive
    expect(disconnectCount).toBe(0);

    client.disconnect();
  });

  it('should not trigger any callback for heartbeat message', async () => {
    const tokenProvider = { getToken: async () => 'test-token' };
    const client = new RealtimeClient('ws://localhost:8080/realtime/ws', tokenProvider as any);

    let connectCount = 0;
    let eventCount = 0;
    let snapshotCount = 0;
    let errorCount = 0;

    client.on('onConnect', () => { connectCount++; });
    client.on('onEvent', () => { eventCount++; });
    client.on('onSnapshot', () => { snapshotCount++; });
    client.on('onError', () => { errorCount++; });

    await client.connect();

    // @ts-ignore - access private ws for testing
    const ws = client['ws'] as MockWebSocket;
    
    // Reset connect count after initial connection
    connectCount = 0;

    // Simulate multiple heartbeat messages
    ws.simulateMessage({ type: 'heartbeat' });
    ws.simulateMessage({ type: 'heartbeat' });
    ws.simulateMessage({ type: 'heartbeat' });

    // Heartbeat should not trigger any user-facing callbacks
    expect(connectCount).toBe(0);
    expect(eventCount).toBe(0);
    expect(snapshotCount).toBe(0);
    expect(errorCount).toBe(0);

    client.disconnect();
  });
});
