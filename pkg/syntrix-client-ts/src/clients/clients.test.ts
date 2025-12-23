import { describe, it, expect, mock, afterEach } from 'bun:test';
import { SyntrixClient } from './syntrix-client';
import { TriggerClient } from './trigger-client';
import axios from 'axios';

const originalCreate = axios.create;

describe('SyntrixClient', () => {
  afterEach(() => {
    axios.create = originalCreate;
  });

  it('should create collection reference', () => {
    const client = new SyntrixClient('http://localhost', {});
    const col = client.collection('users');
    expect(col.path).toBe('users');
  });

  it('should create doc reference from path', () => {
    const client = new SyntrixClient('http://localhost', {});
    const doc = client.doc('users/123');
    expect(doc.path).toBe('users/123');
    expect(doc.id).toBe('123');
  });
});

describe('TriggerClient', () => {
  afterEach(() => {
    axios.create = originalCreate;
  });

  it('should reject add without ID', async () => {
    const client = new TriggerClient('http://localhost', 'token');
    const col = client.collection('users');
    try {
        await col.add({});
        expect(true).toBe(false);
    } catch (e: any) {
        expect(e.message).toContain('explicit ID');
    }
  });

  it('should send batch writes', async () => {
    let capturedConfig: any;
    let capturedData: any;

    const mockAxiosInstance = {
        post: mock(async (url, data) => {
            capturedData = data;
            return { data: {} };
        }),
        interceptors: {
            request: { use: () => {} },
            response: { use: () => {} }
        }
    };

    axios.create = mock((config) => {
        capturedConfig = config;
        return mockAxiosInstance as any;
    }) as any;

    const client = new TriggerClient('http://localhost', 'token');
    const writes = [{ type: 'create', path: 'users/1', data: {} }];
    await client.batch(writes);

    expect(capturedConfig.baseURL).toBe('http://localhost/v1/trigger');
    expect(capturedConfig.headers.Authorization).toBe('Bearer token');
    expect(mockAxiosInstance.post).toHaveBeenCalledWith('write', { writes });
  });

  it('should create doc reference', () => {
    const client = new TriggerClient('http://localhost', 'token');
    const doc = client.doc('users/123');
    expect(doc.path).toBe('users/123');
    expect(doc.id).toBe('123');
  });
});
