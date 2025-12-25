import { TokenProvider } from '../internal/auth/types';

// Message types matching server protocol
export const MessageType = {
  Auth: 'auth',
  AuthAck: 'auth_ack',
  Subscribe: 'subscribe',
  SubscribeAck: 'subscribe_ack',
  Unsubscribe: 'unsubscribe',
  UnsubscribeAck: 'unsubscribe_ack',
  Event: 'event',
  Snapshot: 'snapshot',
  Error: 'error',
} as const;

export interface BaseMessage {
  id?: string;
  type: string;
  payload?: any;
}

export interface SubscribeQuery {
  collection: string;
  filters?: Array<{ field: string; op: string; value: any }>;
}

export interface SubscribeOptions {
  query: SubscribeQuery;
  includeData?: boolean;
  sendSnapshot?: boolean;
}

export interface RealtimeEvent {
  subId: string;
  delta: {
    type: 'create' | 'update' | 'delete';
    id: string;
    document?: Record<string, any>;
    timestamp: number;
  };
}

export interface SnapshotEvent {
  subId: string;
  documents: Record<string, any>[];
}

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export interface RealtimeCallbacks {
  onConnect?: () => void;
  onDisconnect?: () => void;
  onError?: (error: Error) => void;
  onEvent?: (event: RealtimeEvent) => void;
  onSnapshot?: (snapshot: SnapshotEvent) => void;
  onStateChange?: (state: ConnectionState) => void;
}

export class RealtimeClient {
  private ws: WebSocket | null = null;
  private wsUrl: string;
  private tokenProvider: TokenProvider;
  private callbacks: RealtimeCallbacks = {};
  private subscriptions: Map<string, SubscribeOptions> = new Map();
  private messageHandlers: Map<string, (msg: BaseMessage) => void> = new Map();
  private subIdCounter = 0;
  private state: ConnectionState = 'disconnected';
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectDelay = 1000;

  constructor(wsUrl: string, tokenProvider: TokenProvider) {
    this.wsUrl = wsUrl;
    this.tokenProvider = tokenProvider;
  }

  on<K extends keyof RealtimeCallbacks>(event: K, callback: RealtimeCallbacks[K]): this {
    this.callbacks[event] = callback;
    return this;
  }

  private setState(newState: ConnectionState) {
    if (this.state !== newState) {
      this.state = newState;
      this.callbacks.onStateChange?.(newState);
    }
  }

  async connect(): Promise<void> {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      return;
    }

    this.setState('connecting');

    return new Promise((resolve, reject) => {
      try {
        this.ws = new WebSocket(this.wsUrl);

        this.ws.onopen = async () => {
          this.reconnectAttempts = 0;
          // Send auth message
          const token = await this.tokenProvider.getToken();
          if (token) {
            this.sendMessage({
              id: 'auth-init',
              type: MessageType.Auth,
              payload: { token },
            });
          }
          this.setState('connected');
          this.callbacks.onConnect?.();
          
          // Resubscribe to existing subscriptions
          for (const [subId, options] of this.subscriptions) {
            this.sendSubscribe(subId, options);
          }
          
          resolve();
        };

        this.ws.onmessage = (event) => {
          try {
            const msg: BaseMessage = JSON.parse(event.data);
            this.handleMessage(msg);
          } catch (error) {
            console.error('[Realtime] Failed to parse message:', error);
          }
        };

        this.ws.onerror = (event) => {
          this.setState('error');
          const error = new Error('WebSocket error');
          this.callbacks.onError?.(error);
          reject(error);
        };

        this.ws.onclose = () => {
          this.setState('disconnected');
          this.callbacks.onDisconnect?.();
          this.attemptReconnect();
        };
      } catch (error) {
        this.setState('error');
        reject(error);
      }
    });
  }

  private attemptReconnect() {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      return;
    }

    this.reconnectAttempts++;
    const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1);
    
    setTimeout(() => {
      if (this.state === 'disconnected') {
        this.connect().catch(() => {});
      }
    }, delay);
  }

  disconnect(): void {
    if (this.ws) {
      this.reconnectAttempts = this.maxReconnectAttempts; // Prevent reconnect
      this.ws.close();
      this.ws = null;
    }
    this.setState('disconnected');
  }

  private sendMessage(msg: BaseMessage): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  private handleMessage(msg: BaseMessage): void {
    // Check for pending handler
    if (msg.id && this.messageHandlers.has(msg.id)) {
      const handler = this.messageHandlers.get(msg.id)!;
      this.messageHandlers.delete(msg.id);
      handler(msg);
      return;
    }

    switch (msg.type) {
      case MessageType.AuthAck:
        // Auth successful
        break;
      case MessageType.Event:
        if (msg.payload) {
          // Payload is already parsed JSON object
          const event: RealtimeEvent = typeof msg.payload === 'string' 
            ? JSON.parse(msg.payload) 
            : msg.payload;
          this.callbacks.onEvent?.(event);
        }
        break;
      case MessageType.Snapshot:
        if (msg.payload) {
          // Payload is already parsed JSON object
          const snapshot: SnapshotEvent = typeof msg.payload === 'string'
            ? JSON.parse(msg.payload)
            : msg.payload;
          this.callbacks.onSnapshot?.(snapshot);
        }
        break;
      case MessageType.Error:
        if (msg.payload) {
          const error = typeof msg.payload === 'string'
            ? JSON.parse(msg.payload)
            : msg.payload;
          this.callbacks.onError?.(new Error(error.message));
        }
        break;
    }
  }

  subscribe(options: SubscribeOptions): string {
    const subId = `sub-${++this.subIdCounter}`;
    this.subscriptions.set(subId, options);

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.sendSubscribe(subId, options);
    }

    return subId;
  }

  private sendSubscribe(subId: string, options: SubscribeOptions): void {
    this.sendMessage({
      id: subId,
      type: MessageType.Subscribe,
      payload: {
        query: options.query,
        includeData: options.includeData ?? true,
        sendSnapshot: options.sendSnapshot ?? false,
      },
    });
  }

  unsubscribe(subId: string): void {
    this.subscriptions.delete(subId);
    this.sendMessage({
      id: `unsub-${subId}`,
      type: MessageType.Unsubscribe,
      payload: { id: subId },
    });
  }

  getState(): ConnectionState {
    return this.state;
  }
}

// Legacy class for backward compatibility
export class RealtimeListener {
  private client: RealtimeClient;

  constructor(wsUrl: string, tokenProvider: TokenProvider) {
    this.client = new RealtimeClient(wsUrl, tokenProvider);
  }

  connect() {
    this.client.connect();
  }

  disconnect() {
    this.client.disconnect();
  }

  onEvent(callback: (event: any) => void) {
    this.client.on('onEvent', callback);
  }
}
