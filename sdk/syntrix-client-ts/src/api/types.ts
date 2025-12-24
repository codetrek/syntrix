export interface DocumentReference<T> {
  id: string;
  path: string;
  get(): Promise<T | null>;
  set(data: T): Promise<T>;
  update(data: Partial<T>): Promise<T>;
  delete(): Promise<void>;
  collection<U>(path: string): CollectionReference<U>;
}

export interface Query<T> {
  where(field: string, op: string, value: any): Query<T>;
  orderBy(field: string, direction?: 'asc' | 'desc'): Query<T>;
  limit(n: number): Query<T>;
  get(): Promise<T[]>;
}

export interface CollectionReference<T> extends Query<T> {
  path: string;
  doc(id?: string): DocumentReference<T>;
  add(data: T): Promise<DocumentReference<T>>;
}
