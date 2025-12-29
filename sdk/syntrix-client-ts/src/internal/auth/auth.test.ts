import { describe, it, expect, mock, afterEach } from 'bun:test';
import { DefaultTokenProvider } from './provider';
import axios from 'axios';

const originalPost = axios.post;

describe('DefaultTokenProvider', () => {
  afterEach(() => {
    axios.post = originalPost;
  });

  it('should call derived /login endpoint with tenant and set tokens', async () => {
    const postMock = mock(async (url: string, body: any) => {
      expect(url).toBe('http://auth/auth/v1/login');
      expect(body).toEqual({ username: 'alice', password: 'pw', tenant: 't1' });
      return { data: { access_token: 'at', refresh_token: 'rt', expires_in: 3600 } };
    }) as any;
    axios.post = postMock;

    const provider = new DefaultTokenProvider({ refreshUrl: 'http://auth/auth/v1/refresh', tenantId: 't1' });
    const resp = await provider.login('alice', 'pw');

    expect(resp.access_token).toBe('at');
    expect(resp.refresh_token).toBe('rt');
    expect(await provider.getToken()).toBe('at');
  });

  it('should call derived /logout endpoint when refresh token exists', async () => {
    const postMock = mock(async (url: string, body: any) => {
      expect(url).toBe('http://auth/auth/v1/logout');
      expect(body).toEqual({ refresh_token: 'rt' });
      return { data: {} };
    }) as any;
    axios.post = postMock;

    const provider = new DefaultTokenProvider({ refreshToken: 'rt', refreshUrl: 'http://auth/auth/v1/refresh' });
    await provider.logout();

    expect(postMock).toHaveBeenCalled();
    expect(await provider.getToken()).toBeNull();
  });

  it('should serialize refresh calls', async () => {
    let callCount = 0;
    axios.post = mock(async () => {
        callCount++;
        await new Promise(resolve => setTimeout(resolve, 50));
        return { data: { access_token: 'new-token', refresh_token: 'new-refresh' } };
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
