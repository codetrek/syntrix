import axios from 'axios';
import { AuthConfig } from '../internal/auth/types';
import { DefaultTokenProvider } from '../internal/auth/provider';
import { setupAuthInterceptor } from '../internal/auth/interceptor';
import { RestTransport } from '../internal/transport/rest-transport';
import { StorageClient } from '../internal/storage-client';
import { CollectionReference, DocumentReference } from '../api/types';
import { CollectionReferenceImpl, DocumentReferenceImpl } from '../api/reference';

export class SyntrixClient {
  private storage: StorageClient;

  constructor(baseUrl: string, authConfig: AuthConfig) {
    const axiosInstance = axios.create({ baseURL: baseUrl });
    const tokenProvider = new DefaultTokenProvider(authConfig);
    setupAuthInterceptor(axiosInstance, tokenProvider);
    this.storage = new RestTransport(axiosInstance);
  }

  collection<T>(path: string): CollectionReference<T> {
    return new CollectionReferenceImpl<T>(this.storage, path);
  }

  doc<T>(path: string): DocumentReference<T> {
      const parts = path.split('/');
      const id = parts[parts.length - 1];
      return new DocumentReferenceImpl<T>(this.storage, path, id);
  }
}
