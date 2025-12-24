import axios from 'axios';
import { RestTransport } from '../internal/transport/rest-transport';
import { StorageClient } from '../internal/storage-client';
import { CollectionReference, DocumentReference } from '../api/types';
import { CollectionReferenceImpl, DocumentReferenceImpl } from '../api/reference';

class TriggerCollectionReferenceImpl<T> extends CollectionReferenceImpl<T> {
  async add(data: T): Promise<DocumentReference<T>> {
      throw new Error('TriggerClient requires explicit ID for creation. Use doc(id).set(data) instead.');
  }
}

export class TriggerClient {
  private storage: StorageClient;

  constructor(baseUrl: string, token: string) {
    const cleanBaseUrl = baseUrl.replace(/\/$/, '');
    const axiosInstance = axios.create({
        baseURL: `${cleanBaseUrl}/api/v1/trigger`,
        headers: { Authorization: `Bearer ${token}` }
    });
    this.storage = new RestTransport(axiosInstance);
  }

  collection<T>(path: string): CollectionReference<T> {
    return new TriggerCollectionReferenceImpl<T>(this.storage, path);
  }

  doc<T>(path: string): DocumentReference<T> {
      const parts = path.split('/');
      const id = parts[parts.length - 1];
      return new DocumentReferenceImpl<T>(this.storage, path, id);
  }

  async batch(writes: any[]): Promise<void> {
      await this.storage.create('write', { writes });
  }
}
