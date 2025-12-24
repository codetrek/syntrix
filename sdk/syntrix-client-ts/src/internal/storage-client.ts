export interface StorageClient {
  get<T>(path: string): Promise<T | null>;
  create<T>(path: string, data: T): Promise<T>;
  set<T>(path: string, data: T): Promise<T>;
  update<T>(path: string, data: Partial<T>): Promise<T>;
  delete(path: string): Promise<void>;
  query<T>(path: string, query: any): Promise<T[]>;
}
