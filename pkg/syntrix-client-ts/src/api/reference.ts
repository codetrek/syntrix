import { StorageClient } from '../internal/storage-client';
import { CollectionReference, DocumentReference, Query } from './types';

export class DocumentReferenceImpl<T> implements DocumentReference<T> {
  constructor(private storage: StorageClient, public path: string, public id: string) {}

  async get(): Promise<T | null> {
    return this.storage.get<T>(this.path);
  }

  async set(data: T): Promise<T> {
    return this.storage.set<T>(this.path, data);
  }

  async update(data: Partial<T>): Promise<T> {
    return this.storage.update<T>(this.path, data);
  }

  async delete(): Promise<void> {
    return this.storage.delete(this.path);
  }

  collection<U>(path: string): CollectionReference<U> {
    return new CollectionReferenceImpl<U>(this.storage, `${this.path}/${path}`);
  }
}

export class QueryImpl<T> implements Query<T> {
  protected filters: any[] = [];
  protected sort: any[] = [];
  protected limitVal?: number;

  constructor(protected storage: StorageClient, public path: string) {}

  where(field: string, op: string, value: any): Query<T> {
    this.filters.push({ field, op, value });
    return this;
  }

  orderBy(field: string, direction: 'asc' | 'desc' = 'asc'): Query<T> {
    this.sort.push({ field, direction });
    return this;
  }

  limit(n: number): Query<T> {
    this.limitVal = n;
    return this;
  }

  async get(): Promise<T[]> {
    const query = {
      from: this.path,
      filters: this.filters,
      sort: this.sort,
      limit: this.limitVal
    };
    return this.storage.query<T>('/api/v1/query', query);
  }
}

export class CollectionReferenceImpl<T> extends QueryImpl<T> implements CollectionReference<T> {
  constructor(storage: StorageClient, path: string) {
    super(storage, path);
  }

  doc(id?: string): DocumentReference<T> {
    if (id) {
      return new DocumentReferenceImpl<T>(this.storage, `${this.path}/${id}`, id);
    }
    const autoId = crypto.randomUUID();
    return new DocumentReferenceImpl<T>(this.storage, `${this.path}/${autoId}`, autoId);
  }

  async add(data: T): Promise<DocumentReference<T>> {
    const result: any = await this.storage.create<T>(this.path, data);
    const id = result.id;
    if (!id) {
        throw new Error('Server did not return an ID for the created document');
    }
    return new DocumentReferenceImpl<T>(this.storage, `${this.path}/${id}`, id);
  }
}
