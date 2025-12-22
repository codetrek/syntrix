import { createRxDatabase, RxDatabase, RxCollection, addRxPlugin } from 'rxdb';
import { getRxStorageDexie } from 'rxdb/plugins/storage-dexie';
import { RxDBDevModePlugin } from 'rxdb/plugins/dev-mode';
import { RxDBUpdatePlugin } from 'rxdb/plugins/update';
import { RxDBQueryBuilderPlugin } from 'rxdb/plugins/query-builder';
import { replicateRxCollection } from 'rxdb/plugins/replication';
import { API_URL, RT_URL } from './constants';
import { getAuthToken, getUserId, onLogout, fetchWithAuth } from './auth';

addRxPlugin(RxDBDevModePlugin);
addRxPlugin(RxDBUpdatePlugin);
addRxPlugin(RxDBQueryBuilderPlugin);

// Register logout handler
onLogout(async () => {
    if (dbPromise) {
        const db = await dbPromise;
        await db.destroy();
        dbPromise = null;
    }
});

// --- Schemas ---

export type Chat = {
    id: string;
    title: string;
    updatedAt: number;
};

export type Message = {
    id: string;
    role: 'user' | 'assistant' | 'system' | 'tool';
    content: string;
    createdAt: number;
};

export type ToolCall = {
    id: string;
    toolName: string;
    args: any;
    status: 'pending' | 'success' | 'failed';
    result?: string;
    error?: string;
    createdAt: number;
};

const chatSchema = {
    title: 'chat schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        title: { type: 'string' },
        updatedAt: { type: 'number' }
    },
    required: ['id', 'title', 'updatedAt']
};

const messageSchema = {
    title: 'message schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        role: { type: 'string' },
        content: { type: 'string' },
        createdAt: { type: 'number' }
    },
    required: ['id', 'role', 'createdAt']
};

const toolCallSchema = {
    title: 'tool call schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        toolName: { type: 'string' },
        args: { type: 'object' },
        status: { type: 'string' },
        result: { type: 'string' },
        error: { type: 'string' },
        createdAt: { type: 'number' }
    },
    required: ['id', 'toolName', 'status', 'createdAt']
};

// --- Database Type ---

export type MyDatabaseCollections = {
    chats: RxCollection<Chat>;
    [key: string]: RxCollection<any>; // Allow dynamic collections
};

export type MyDatabase = RxDatabase<MyDatabaseCollections>;

// --- Replication Logic ---

class RealtimeManager {
    private ws: WebSocket | null = null;
    private subscriptions: Map<string, { query: any, includeData: boolean, callback: () => void }> = new Map();
    private url: string;

    constructor(url: string) {
        this.url = url;
    }

    connect() {
        if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) return;

        this.ws = new WebSocket(this.url);

        this.ws.onopen = () => {
            console.log('WS Connected');
            this.subscriptions.forEach((sub, id) => {
                this.sendSubscribe(id, sub);
            });
        };

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'event') {
                    const payload = msg.payload;
                    const subId = payload.subId;
                    if (subId && this.subscriptions.has(subId)) {
                        this.subscriptions.get(subId)?.callback();
                    }
                }
            } catch (e) {
                console.error('WS Error', e);
            }
        };

        this.ws.onclose = () => {
            this.ws = null;
            setTimeout(() => this.connect(), 3000);
        };
    }

    subscribe(query: any, callback: () => void): string {
        const id = crypto.randomUUID();
        const sub = { query, includeData: false, callback };
        this.subscriptions.set(id, sub);

        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.sendSubscribe(id, sub);
        } else {
            this.connect();
        }
        return id;
    }

    private sendSubscribe(id: string, sub: { query: any, includeData: boolean }) {
        this.ws?.send(JSON.stringify({
            id: id,
            type: 'subscribe',
            payload: {
                query: sub.query,
                includeData: sub.includeData
            }
        }));
    }

    unsubscribe(id: string) {
        this.subscriptions.delete(id);
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify({
                id: crypto.randomUUID(),
                type: 'unsubscribe',
                payload: { id }
            }));
        }
    }
}

const realtimeManager = new RealtimeManager(RT_URL.replace('http', 'ws') + '/v1/realtime');

// Helper to create a replication state for a collection
const setupReplication = async (collection: RxCollection, remoteCollectionPath: string) => {
    const replicationState = replicateRxCollection({
        collection,
        replicationIdentifier: `sync-${remoteCollectionPath}`,
        pull: {
            handler: async (checkpoint: any, batchSize: number) => {
                const updatedAt = checkpoint ? checkpoint.updatedAt : 0;
                const response = await fetchWithAuth(`${API_URL}/v1/replication/pull?collection=${encodeURIComponent(remoteCollectionPath)}&checkpoint=${updatedAt}&limit=${batchSize}`);
                const data = await response.json();
                return {
                    documents: data.documents.map((doc: any) => ({
                        ...doc,
                        id: doc.id, // Ensure ID is mapped
                        updatedAt: doc.updatedAt, // Map snake_case to camelCase
                        version: doc.version // Map version
                    })),
                    checkpoint: data.checkpoint ? { updatedAt: data.checkpoint } : null
                };
            }
        },
        push: {
            handler: async (docs) => {
                const changes = docs.map(d => {
                    // Determine action
                    // Note: Syntrix currently treats create/update similarly (Upsert) via ReplaceDocument logic in some places,
                    // but let's try to be specific if possible.
                    // However, RxDB push rows don't explicitly say "create" vs "update" easily without checking assumedMasterState.
                    // For simplicity, we can default to "update" or "create" as Syntrix might handle them as upsert.
                    // Let's check server_replica_handler.go again. It seems it just takes the doc and appends to changes.
                    // It doesn't seem to use 'Action' field heavily in the snippet I read, but let's provide it.

                    return {
                        action: 'update', // Default to update/upsert
                        document: {
                            ...d.newDocumentState,
                            id: (d.newDocumentState as any).id,
                            version: (d.newDocumentState as any).version || 0 // Syntrix handles versioning
                        }
                    };
                });

                const response = await fetchWithAuth(`${API_URL}/v1/replication/push`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        collection: remoteCollectionPath,
                        changes: changes
                    })
                });
                if (!response.ok) {
                    throw new Error('Push failed');
                }
                // Return conflict-free (Syntrix is LWW for now)
                return [];
            }
        },
        live: true,
        retryTime: 5000,
    });

    // WebSocket for Realtime
    // Use shared RealtimeManager
    let debounceTimer: any;
    const subId = realtimeManager.subscribe({ collection: remoteCollectionPath }, () => {
        if (debounceTimer) clearTimeout(debounceTimer);
        debounceTimer = setTimeout(() => {
            replicationState.reSync();
        }, 100);
    });

    return {
        replicationState,
        cancel: async () => {
            if (debounceTimer) clearTimeout(debounceTimer);
            await replicationState.cancel();
            realtimeManager.unsubscribe(subId);
        }
    };
};

let dbPromise: Promise<MyDatabase> | null = null;

export const getDatabase = async (): Promise<MyDatabase> => {
    const currentUserId = getUserId();
    if (!currentUserId) {
        throw new Error("User not logged in");
    }

    if (dbPromise) {
        return dbPromise;
    }

    dbPromise = createRxDatabase<MyDatabaseCollections>({
        name: `chatdb_${currentUserId}`, // New DB name to avoid conflicts
        storage: getRxStorageDexie(),
        ignoreDuplicate: true
    }).then(async (db) => {
        // 1. Add Chats Collection
        await db.addCollections({
            chats: { schema: chatSchema }
        });

        // 2. Start Sync for Chats
        await setupReplication(db.chats, `users/${currentUserId}/chats`);

        return db;
    });

    return dbPromise;
};

// Dynamic Collection Manager
export const getMessagesCollection = async (chatId: string): Promise<RxCollection<Message>> => {
    const db = await getDatabase();
    const collectionName = `messages_${chatId}`;

    // Check if already exists
    if (db[collectionName]) {
        return db[collectionName];
    }

    // Create Collection
    const collections = await db.addCollections({
        [collectionName]: { schema: messageSchema }
    });
    return collections[collectionName];
};

export const getToolCallsCollection = async (chatId: string): Promise<RxCollection<ToolCall>> => {
    const db = await getDatabase();
    const collectionName = `toolcalls_${chatId}`;

    // Check if already exists
    if (db[collectionName]) {
        return db[collectionName];
    }

    // Create Collection
    const collections = await db.addCollections({
        [collectionName]: { schema: toolCallSchema }
    });
    return collections[collectionName];
};

export const startChatSync = async (chatId: string) => {
    const currentUserId = getUserId();
    if (!currentUserId) throw new Error("Not logged in");
    const collection = await getMessagesCollection(chatId);
    const remotePath = `users/${currentUserId}/chats/${chatId}/messages`;
    return setupReplication(collection, remotePath);
};

export const startToolCallSync = async (chatId: string) => {
    const currentUserId = getUserId();
    if (!currentUserId) throw new Error("Not logged in");
    const collection = await getToolCallsCollection(chatId);
    const remotePath = `users/${currentUserId}/chats/${chatId}/toolcall`;
    return setupReplication(collection, remotePath);
};

// Deprecated: Use getMessagesCollection + startChatSync
export const syncMessages = async (chatId: string): Promise<RxCollection<Message>> => {
    const currentUserId = getUserId();
    if (!currentUserId) throw new Error("Not logged in");
    const collection = await getMessagesCollection(chatId);
    const remotePath = `users/${currentUserId}/chats/${chatId}/messages`;
    // Note: This starts sync but doesn't return the cancel function, so it leaks if used repeatedly.
    // Kept for backward compatibility if needed, but ChatWindow should be updated.
    await setupReplication(collection, remotePath);
    return collection;
};
