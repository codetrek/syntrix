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

// --- Database Type ---

export type MyDatabaseCollections = {
    chats: RxCollection<Chat>;
    [key: string]: RxCollection<any>; // Allow dynamic collections
};

export type MyDatabase = RxDatabase<MyDatabaseCollections>;

// --- Replication Logic ---

// Helper to create a replication state for a collection
const setupReplication = async (collection: RxCollection, remoteCollectionPath: string) => {
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
        },
        live: true,
        retryTime: 5000,
    });

    // WebSocket for Realtime
    // Note: Syntrix Realtime API might need a specific handshake or subscription
    // For this demo, we'll rely on polling (pull.live = true does polling if stream not provided?)
    // Actually replicateRxCollection 'live: true' just keeps the replication state open.
    // We need to trigger re-pull on WS event.

    const wsUrl = RT_URL.replace('http', 'ws') + '/v1/realtime';
    const ws = new WebSocket(wsUrl);
    ws.onopen = () => {
        // Subscribe to collection
        ws.send(JSON.stringify({
            type: 'subscribe',
            payload: {
                query: {
                    collection: remoteCollectionPath
                },
                includeData: false // Optimization: We only need the event to trigger re-sync
            }
        }));
    };
    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === 'event') {
            replicationState.reSync();
        }
    };

    return replicationState;
};

let dbPromise: Promise<MyDatabase> | null = null;

export const getDatabase = async (): Promise<MyDatabase> => {
    if (dbPromise) {
        return dbPromise;
    }

    dbPromise = createRxDatabase<MyDatabaseCollections>({
        name: 'chatdb_v2', // New DB name to avoid conflicts
        storage: getRxStorageDexie()
    }).then(async (db) => {
        // 1. Add Chats Collection
        await db.addCollections({
            chats: { schema: chatSchema }
        });

        // 2. Start Sync for Chats
        await setupReplication(db.chats, `users/${USER_ID}/chats`);

        return db;
    });

    return dbPromise;
};

// Dynamic Collection Manager
export const syncMessages = async (chatId: string): Promise<RxCollection<Message>> => {
    const db = await getDatabase();
    const collectionName = `messages_${chatId}`;
    const remotePath = `users/${USER_ID}/chats/${chatId}/messages`;

    // Check if already exists
    if (db[collectionName]) {
        return db[collectionName];
    }

    // Create Collection
    const collections = await db.addCollections({
        [collectionName]: { schema: messageSchema }
    });
    const collection = collections[collectionName];

    // Start Sync
    await setupReplication(collection, remotePath);

    return collection;
};
