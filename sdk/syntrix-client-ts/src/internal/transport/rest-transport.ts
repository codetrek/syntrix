import { AxiosInstance } from 'axios';
import { StorageClient } from '../storage-client';

export class RestTransport implements StorageClient {
  constructor(private axios: AxiosInstance) {}

  async get<T>(path: string): Promise<T | null> {
    try {
      const response = await this.axios.get(path);
      return response.data;
    } catch (error: any) {
      if (error.response && error.response.status === 404) {
        return null;
      }
      throw error;
    }
  }

  async create<T>(path: string, data: T): Promise<T> {
    const response = await this.axios.post(path, data);
    return response.data;
  }

  async set<T>(path: string, data: T): Promise<T> {
    const response = await this.axios.put(path, data);
    return response.data;
  }

  async update<T>(path: string, data: Partial<T>): Promise<T> {
    const response = await this.axios.patch(path, data);
    return response.data;
  }

  async delete(path: string): Promise<void> {
    await this.axios.delete(path);
  }

  async query<T>(path: string, query: any): Promise<T[]> {
    const response = await this.axios.post(path, query);
    if (response.data && Array.isArray(response.data.docs)) {
        return response.data.docs;
    }
    return response.data;
  }
}
