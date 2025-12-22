export interface Document<T = any> {
    id: string;
    fullpath: string;
    collection: string;
    parent: string;
    updatedAt: number;
    createdAt: number;
    version: number;
    data: T;
    deleted?: boolean;
}

export interface Filter {
    field: string;
    op: string;
    value: any;
}

export interface Order {
    field: string;
    direction: 'asc' | 'desc';
}

export interface Query {
    collection: string;
    filters?: Filter[];
    orderBy?: Order[];
    limit?: number;
    startAfter?: string;
    showDeleted?: boolean;
}

export interface WriteOp {
    type: 'create' | 'update' | 'replace' | 'delete';
    path: string;
    data?: any;
}

export interface WriteRequest {
    writes: WriteOp[];
}

export interface GetRequest {
    paths: string[];
}

export interface GetResponse<T = any> {
    documents: T[]; // Flattened documents (data merged with metadata)
}

export interface TriggerPayload<T = any> {
    triggerId: string;
    tenant: string;
    event: 'create' | 'update' | 'delete';
    collection: string;
    docKey: string;
    lsn: string;
    seq: number;
    before?: T;
    after?: T;
    ts: number;
    url: string;
    headers: Record<string, string>;
    secretsRef: string;
    retryPolicy: {
        maxAttempts: number;
        initialBackoff: string;
        maxBackoff: string;
    };
    timeout: string;
    preIssuedToken?: string;
}
