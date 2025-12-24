import { describe, it, expect, mock, afterEach } from 'bun:test';
import { DefaultTokenProvider } from './provider';
import axios from 'axios';

const originalPost = axios.post;

describe('DefaultTokenProvider', () => {
  afterEach(() => {
    axios.post = originalPost;
  });

  it('should serialize refresh calls', async () => {
    let callCount = 0;
    axios.post = mock(async () => {
        callCount++;
        await new Promise(resolve => setTimeout(resolve, 50));
        return { data: { token: 'new-token' } };
    }) as any;

    const provider = new DefaultTokenProvider({
      token: 'old',
      refreshToken: 'refresh',
      refreshUrl: 'http://refresh',
    });

    const p1 = provider.refreshToken();
    const p2 = provider.refreshToken();

    const [t1, t2] = await Promise.all([p1, p2]);

    expect(t1).toBe('new-token');
    expect(t2).toBe('new-token');
    expect(callCount).toBe(1);
  });

  it('should bubble refresh errors', async () => {
    axios.post = mock(async () => {
        throw new Error('Refresh failed');
    }) as any;

    const provider = new DefaultTokenProvider({
      token: 'old',
      refreshToken: 'refresh',
      refreshUrl: 'http://refresh',
    });

    try {
        await provider.refreshToken();
        expect(true).toBe(false); // Should not reach here
    } catch (e: any) {
        expect(e.message).toBe('Refresh failed');
    }
  });
});
