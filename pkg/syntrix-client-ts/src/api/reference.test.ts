import { describe, it, expect, mock } from 'bun:test';
import { CollectionReferenceImpl, DocumentReferenceImpl, QueryImpl } from './reference';
import { StorageClient } from '../internal/storage-client';

describe('Reference API', () => {
  const mockStorage = {
    get: mock(async () => null),
    create: mock(async () => ({ id: 'new-id' })),
    set: mock(async () => ({})),
    update: mock(async () => ({})),
    delete: mock(async () => undefined),
    query: mock(async () => []),
  } as unknown as StorageClient;

  describe('DocumentReference', () => {
    it('should construct correct path', () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      expect(doc.path).toBe('users/123');
      expect(doc.id).toBe('123');
    });

    it('should call storage.get', async () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      await doc.get();
      expect(mockStorage.get).toHaveBeenCalledWith('users/123');
    });

    it('should call storage.set', async () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      const data = { name: 'Alice' };
      await doc.set(data);
      expect(mockStorage.set).toHaveBeenCalledWith('users/123', data);
    });

    it('should call storage.update', async () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      const data = { name: 'Alice' };
      await doc.update(data);
      expect(mockStorage.update).toHaveBeenCalledWith('users/123', data);
    });

    it('should call storage.delete', async () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      await doc.delete();
      expect(mockStorage.delete).toHaveBeenCalledWith('users/123');
    });

    it('should create subcollection reference', () => {
      const doc = new DocumentReferenceImpl(mockStorage, 'users/123', '123');
      const sub = doc.collection('posts');
      expect(sub.path).toBe('users/123/posts');
    });
  });

  describe('CollectionReference', () => {
    it('should create doc reference with ID', () => {
      const col = new CollectionReferenceImpl(mockStorage, 'users');
      const doc = col.doc('123');
      expect(doc.path).toBe('users/123');
      expect(doc.id).toBe('123');
    });

    it('should create doc reference with auto ID', () => {
      const col = new CollectionReferenceImpl(mockStorage, 'users');
      const doc = col.doc();
      expect(doc.path).toMatch(/^users\/.+/);
      expect(doc.id).toBeTruthy();
    });

    it('should call storage.create on add', async () => {
      const col = new CollectionReferenceImpl(mockStorage, 'users');
      const data = { name: 'Bob' };
      const docRef = await col.add(data);
      expect(mockStorage.create).toHaveBeenCalledWith('users', data);
      expect(docRef.id).toBe('new-id');
      expect(docRef.path).toBe('users/new-id');
    });
  });

  describe('Query', () => {
    it('should build query correctly', async () => {
      const col = new CollectionReferenceImpl(mockStorage, 'users');
      await col
        .where('age', '>=', 18)
        .orderBy('name', 'asc')
        .limit(10)
        .get();

      expect(mockStorage.query).toHaveBeenCalledWith('/v1/query', {
        from: 'users',
        filters: [{ field: 'age', op: '>=', value: 18 }],
        sort: [{ field: 'name', direction: 'asc' }],
        limit: 10
      });
    });
  });
});
