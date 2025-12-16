import { createRxDatabase, RxDatabase, RxCollection, addRxPlugin } from 'rxdb';
import { getRxStorageDexie } from 'rxdb/plugins/storage-dexie';
import { RxDBDevModePlugin } from 'rxdb/plugins/dev-mode';
import { RxDBUpdatePlugin } from 'rxdb/plugins/update';
import { RxDBQueryBuilderPlugin } from 'rxdb/plugins/query-builder';
import { replicateRxCollection } from 'rxdb/plugins/replication';
import { Observable, Subject } from 'rxjs';

addRxPlugin(RxDBDevModePlugin);
addRxPlugin(RxDBUpdatePlugin);
addRxPlugin(RxDBQueryBuilderPlugin);

export type Message = {
    id: string;
    text: string;
    sender: string;
    timestamp: number;
};

export type MessageCollection = RxCollection<Message>;

export type MyDatabaseCollections = {
    messages: MessageCollection;
};

export type MyDatabase = RxDatabase<MyDatabaseCollections>;

type Checkpoint = {
  id: string;
};

const messageSchema = {
    title: 'message schema',
    version: 0,
    primaryKey: 'id',
    type: 'object',
    properties: {
        id: {
            type: 'string',
            maxLength: 100
        },
        text: {
            type: 'string'
        },
        sender: {
            type: 'string'
        },
        timestamp: {
            type: 'number'
        }
    },
    required: ['id', 'text', 'sender', 'timestamp']
};

let dbPromise: Promise<MyDatabase> | null = null;

export const API_URL = 'http://localhost:8080';
export const RT_URL = 'http://localhost:8081';
export const COLLECTION_NAME = 'rooms/chatroom-1/messages';

export const getDatabase = async (): Promise<MyDatabase> => {
    if (dbPromise) {
        return dbPromise;
    }

    dbPromise = createRxDatabase<MyDatabaseCollections>({
        name: 'chatdb',
        storage: getRxStorageDexie()
    }).then(async (db) => {
        await db.addCollections({
            messages: {
                schema: messageSchema
            }
        });

        // Setup WebSocket
        const wsUrl = RT_URL.replace('http', 'ws') + '/v1/realtime';
        const ws = new WebSocket(wsUrl);

        const stream$ = new Subject<any>();
        let pullResolver: ((data: any) => void) | null = null;

        ws.onopen = () => {
            console.log('WS Connected');
        };

        ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);

                if (msg.type === 'stream-event') {
                    if (pullResolver) {
                        const payload = msg.payload;
                        const documents = payload.documents.map((doc: any) => ({
                            id: doc.id,
                            text: doc.text,
                            sender: doc.sender,
                            timestamp: doc.timestamp
                        }));

                        pullResolver({
                            documents: documents,
                            checkpoint: { id: payload.checkpoint.toString() }
                        });
                        pullResolver = null;
                    }
                } else if (msg.type === 'event') {
                    const payload = msg.payload;
                    const doc = payload.delta.document;
                    if (doc) {
                         stream$.next({
                             documents: [{
                                id: doc.id,
                                text: doc.text,
                                sender: doc.sender,
                                timestamp: doc.timestamp
                             }],
                             checkpoint: { id: payload.delta.timestamp.toString() }
                         });
                    }
                }
            } catch (e) {
                console.error('WS Error', e);
            }
        };

        const waitForOpen = () => {
            return new Promise<void>((resolve) => {
                if (ws.readyState === WebSocket.OPEN) {
                    resolve();
                } else {
                    ws.addEventListener('open', () => resolve(), { once: true });
                }
            });
        };

        // Setup Replication
        replicateRxCollection({
            collection: db.messages,
            replicationIdentifier: 'syntrix-replication',
            pull: {
                stream$: stream$,
                handler: async (checkpoint: any, _batchSize: number) => {
                    await waitForOpen();
                    const cp = checkpoint ? parseInt(checkpoint.id) : 0;

                    // Send TypeStream request
                    // We use a fixed ID 'stream-sub' so that the backend updates the subscription
                    // instead of creating new ones if this is called multiple times.
                    ws.send(JSON.stringify({
                        id: 'stream-sub',
                        type: 'stream',
                        payload: {
                            collection: COLLECTION_NAME,
                            checkpoint: cp
                        }
                    }));

                    return new Promise<any>((resolve) => {
                        pullResolver = resolve;
                    });
                }
            },
            push: {
                handler: async (docs) => {
                    const changes = docs.map(doc => {
                        const docData = doc.newDocumentState as any;
                        const type = doc.assumedMasterState ? 'update' : 'create';
                        return {
                            type: type,
                            document: {
                                id: docData.id,
                                text: docData.text,
                                sender: docData.sender,
                                timestamp: docData.timestamp
                            }
                        };
                    });

                    const url = `${API_URL}/v1/replication/push`;

                    try {
                        const response = await fetch(url, {
                            method: 'POST',
                            headers: {
                                'Content-Type': 'application/json'
                            },
                            body: JSON.stringify({
                                collection: COLLECTION_NAME,
                                changes: changes
                            })
                        });

                        if (!response.ok) {
                            throw new Error('Failed to push changes');
                        }

                        // We assume no conflicts for this simple example
                        return [];
                    } catch (err) {
                        console.error('Push error:', err);
                        throw err;
                    }
                }
            },
            live: true,
            retryTime: 5000,
        });

        return db;
    });

    return dbPromise;
};
