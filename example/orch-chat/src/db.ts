import { createRxDatabase, RxDatabase, RxCollection, addRxPlugin } from 'rxdb';
import { getRxStorageDexie } from 'rxdb/plugins/storage-dexie';
import { RxDBDevModePlugin } from 'rxdb/plugins/dev-mode';
import { RxDBUpdatePlugin } from 'rxdb/plugins/update';
import { RxDBQueryBuilderPlugin } from 'rxdb/plugins/query-builder';
import { replicateRxCollection } from 'rxdb/plugins/replication';

addRxPlugin(RxDBDevModePlugin);
addRxPlugin(RxDBUpdatePlugin);
addRxPlugin(RxDBQueryBuilderPlugin);

export const API_URL = 'http://localhost:8080';
export const RT_URL = 'http://localhost:8081';
export const USER_ID = 'demo-user';

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

export type AgentTask = {
    id: string;
    userId: string;
    chatId: string;
    type: 'agent' | 'tool';
    name: string;
    instruction: string;
    subAgentId?: string;
    triggerMessageId: string;
    status: 'pending' | 'running' | 'success' | 'failed' | 'waiting';
    result?: string;
    error?: string;
    createdAt: number;
    updatedAt: number;
};

export type SubAgent = {
    id: string;
    userId: string;
    chatId: string;
    taskId: string;
    role: 'general' | 'researcher';
    status: 'active' | 'completed' | 'failed' | 'waiting_for_user';
    summary?: string;
    createdAt: number;
    updatedAt: number;
};

export type SubAgentMessage = {
    id: string;
    userId: string;
    subAgentId: string;
    role: 'system' | 'user' | 'assistant' | 'tool';
    content: string;
    toolCallId?: string;
    toolCalls?: any[];
    createdAt: number;
};

export type SubAgentToolCall = {
    id: string;
    userId: string;
    subAgentId: string;
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

const taskSchema = {
    title: 'task schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        userId: { type: 'string', maxLength: 100 },
        chatId: { type: 'string', maxLength: 100 },
        type: { type: 'string' },
        name: { type: 'string' },
        instruction: { type: 'string' },
        subAgentId: { type: 'string', maxLength: 100 },
        triggerMessageId: { type: 'string', maxLength: 100 },
        status: { type: 'string' },
        result: { type: 'string' },
        error: { type: 'string' },
        createdAt: { type: 'number' },
        updatedAt: { type: 'number' }
    },
    required: ['id', 'userId', 'chatId', 'type', 'status', 'createdAt'],
    indexes: ['chatId', 'triggerMessageId']
};

const subAgentSchema = {
    title: 'sub agent schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        userId: { type: 'string', maxLength: 100 },
        chatId: { type: 'string', maxLength: 100 },
        taskId: { type: 'string', maxLength: 100 },
        role: { type: 'string' },
        status: { type: 'string' },
        summary: { type: 'string' },
        createdAt: { type: 'number' },
        updatedAt: { type: 'number' }
    },
    required: ['id', 'userId', 'taskId', 'status', 'createdAt'],
    indexes: ['taskId']
};

const subAgentMessageSchema = {
    title: 'sub agent message schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        userId: { type: 'string', maxLength: 100 },
        subAgentId: { type: 'string', maxLength: 100 },
        role: { type: 'string' },
        content: { type: 'string' },
        toolCallId: { type: 'string' },
        toolCalls: { type: 'array' },
        createdAt: { type: 'number', multipleOf: 1, minimum: 0, maximum: 100000000000000 }
    },
    required: ['id', 'userId', 'subAgentId', 'role', 'createdAt'],
    indexes: ['subAgentId', 'createdAt']
};

const subAgentToolCallSchema = {
    title: 'sub agent tool call schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: { type: 'string', maxLength: 100 },
        userId: { type: 'string', maxLength: 100 },
        subAgentId: { type: 'string', maxLength: 100 },
        toolName: { type: 'string' },
        args: { type: 'object' },
        status: { type: 'string' },
        result: { type: 'string' },
        error: { type: 'string' },
        createdAt: { type: 'number' }
    },
    required: ['id', 'userId', 'subAgentId', 'toolName', 'status', 'createdAt'],
    indexes: ['subAgentId']
};

// --- Database Type ---

export type MyDatabaseCollections = {
    chats: RxCollection<Chat>;
    tasks: RxCollection<AgentTask>;
    sub_agents: RxCollection<SubAgent>;
    sub_agent_messages: RxCollection<SubAgentMessage>;
    sub_agent_tool_calls: RxCollection<SubAgentToolCall>;
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
const setupReplication = async (collection: RxCollection, remoteCollectionPath: string, options: { readOnly?: boolean } = {}) => {
    const pushConfig = options.readOnly ? undefined : {
        handler: async (docs: any[]) => {
            const changes = docs.map(d => {
                return {
                    action: 'update', // Default to update/upsert
                    document: {
                        ...d.newDocumentState,
                        id: (d.newDocumentState as any).id,
                        _version: 0 // Syntrix handles versioning
                    }
                };
            });

            const response = await fetch(`${API_URL}/v1/replication/push`, {
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
    };

    const replicationState = replicateRxCollection({
        collection,
        replicationIdentifier: `sync-${remoteCollectionPath}`,
        pull: {
            handler: async (checkpoint: any, batchSize: number) => {
                const updatedAt = checkpoint ? checkpoint.updatedAt : 0;
                const response = await fetch(`${API_URL}/v1/replication/pull?collection=${encodeURIComponent(remoteCollectionPath)}&checkpoint=${updatedAt}&limit=${batchSize}`);
                const data = await response.json();
                return {
                    documents: data.documents.map((doc: any) => ({
                        ...doc,
                        id: doc.id // Ensure ID is mapped
                    })),
                    checkpoint: data.checkpoint ? { updatedAt: data.checkpoint } : null
                };
            }
        },
        push: pushConfig,
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
    if (dbPromise) {
        return dbPromise;
    }

    dbPromise = createRxDatabase<MyDatabaseCollections>({
        name: 'chatdb_v3', // New DB name to avoid conflicts
        storage: getRxStorageDexie(),
        ignoreDuplicate: true
    }).then(async (db) => {
        // 1. Add Collections
        await db.addCollections({
            chats: { schema: chatSchema },
            tasks: { schema: taskSchema },
            sub_agents: { schema: subAgentSchema },
            sub_agent_messages: { schema: subAgentMessageSchema },
            sub_agent_tool_calls: { schema: subAgentToolCallSchema }
        });

        // 2. Start Sync
        await setupReplication(db.chats, `users/${USER_ID}/orch-chats`);

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
    const db = await getDatabase();

    // Sync Messages (Legacy/Current)
    const msgCollection = await getMessagesCollection(chatId);
    const msgRemotePath = `users/${USER_ID}/orch-chats/${chatId}/messages`;
    const msgSync = await setupReplication(msgCollection, msgRemotePath); // Writable (User Input)

    // Sync Tasks
    // We sync tasks for this chat into the flat 'tasks' collection
    // Note: This assumes the backend allows syncing `users/${USER_ID}/orch-chats/${chatId}/tasks`
    const taskRemotePath = `users/${USER_ID}/orch-chats/${chatId}/tasks`;
    const taskSync = await setupReplication(db.tasks, taskRemotePath, { readOnly: true });

    // Sync SubAgents
    const agentRemotePath = `users/${USER_ID}/orch-chats/${chatId}/sub-agents`;
    const agentSync = await setupReplication(db.sub_agents, agentRemotePath, { readOnly: true });

    // We also need to sync messages/tools for sub-agents, but that might be too much to sync ALL of them for a chat immediately?
    // The UX says "Sub-Agent Inspector (When opened)".
    // So we should probably only sync sub-agent messages when the inspector is opened.
    // BUT, for the "Task Block" to show status, we need Tasks.

    return {
        cancel: async () => {
            await msgSync.cancel();
            await taskSync.cancel();
            await agentSync.cancel();
        }
    };
};

export const startSubAgentSync = async (chatId: string, subAgentId: string) => {
    const db = await getDatabase();

    const msgRemotePath = `users/${USER_ID}/orch-chats/${chatId}/sub-agents/${subAgentId}/messages`;
    const msgSync = await setupReplication(db.sub_agent_messages, msgRemotePath, { readOnly: true });

    const toolRemotePath = `users/${USER_ID}/orch-chats/${chatId}/sub-agents/${subAgentId}/tool-calls`;
    const toolSync = await setupReplication(db.sub_agent_tool_calls, toolRemotePath, { readOnly: true });

    return {
        cancel: async () => {
            await msgSync.cancel();
            await toolSync.cancel();
        }
    };
};

export const startToolCallSync = async (chatId: string) => {
    const collection = await getToolCallsCollection(chatId);
    const remotePath = `users/${USER_ID}/orch-chats/${chatId}/toolcall`;
    return setupReplication(collection, remotePath, { readOnly: true });
};

// Deprecated: Use getMessagesCollection + startChatSync
export const syncMessages = async (chatId: string): Promise<RxCollection<Message>> => {
    const collection = await getMessagesCollection(chatId);
    const remotePath = `users/${USER_ID}/orch-chats/${chatId}/messages`;
    // Note: This starts sync but doesn't return the cancel function, so it leaks if used repeatedly.
    // Kept for backward compatibility if needed, but ChatWindow should be updated.
    await setupReplication(collection, remotePath);
    return collection;
};
