import { describe, it, expect, mock } from 'bun:test';
import axios, { AxiosError } from 'axios';
import { setupAuthInterceptor } from './interceptor';
import { TokenProvider } from './types';

describe('AuthInterceptor', () => {
  it('should attach token to request', async () => {
    const mockProvider = {
      getToken: mock(async () => 'test-token'),
      refreshToken: mock(async () => 'new-token'),
      setToken: () => {},
    } as TokenProvider;

    const instance = axios.create();
    // Mock adapter logic is tricky with axios instances directly in bun test without a library like axios-mock-adapter.
    // Instead, we can inspect the interceptor chain or mock the internal request execution if possible.
    // Or simpler: just verify the interceptor is registered and runs logic.

    setupAuthInterceptor(instance, mockProvider);

    // We can manually invoke the request interceptor
    const reqInterceptor = (instance.interceptors.request as any).handlers[0].fulfilled;
    const config = await reqInterceptor({ headers: {} });
    expect(config.headers.Authorization).toBe('Bearer test-token');
  });

  it('should retry on 401', async () => {
    const mockProvider = {
      getToken: mock(async () => 'old-token'),
      refreshToken: mock(async () => 'refreshed-token'),
      setToken: () => {},
    } as TokenProvider;

    const instance = axios.create();
    // Mock the instance itself to return success on retry
    const mockRequest = mock(async (config) => ({ data: 'success' }));
    // We can't easily replace the instance call inside the interceptor closure without more complex mocking.
    // However, we can test the error interceptor logic directly.

    setupAuthInterceptor(instance, mockProvider);
    const errInterceptor = (instance.interceptors.response as any).handlers[0].rejected;

    // Mock the axios instance call that happens inside the interceptor
    // This is a bit hacky because `instance(config)` is called.
    // We can spy on the instance if we wrap it or attach the interceptor to a mock.

    // Let's try a different approach: pass a mock function as the axios instance?
    // setupAuthInterceptor expects AxiosInstance which is a function + properties.

    let retried = false;
    const mockInstance: any = async (config: any) => {
        retried = true;
        return { data: 'retried-success' };
    };
    mockInstance.interceptors = {
        request: { use: () => {} },
        response: { use: () => {} }
    };

    // Re-setup with our mock instance
    setupAuthInterceptor(mockInstance, mockProvider);

    // But wait, setupAuthInterceptor calls `axiosInstance(config)`.
    // So if we pass mockInstance, it should work.

    // We need to manually trigger the error handler that was registered.
    // Since we mocked `use`, we need to capture the handler.
    let capturedErrorHandler: any;
    mockInstance.interceptors.response.use = (success: any, error: any) => {
        capturedErrorHandler = error;
    };

    setupAuthInterceptor(mockInstance, mockProvider);

    const error: any = new Error('401');
    error.response = { status: 401 };
    error.config = { headers: {} };

    const result = await capturedErrorHandler(error);

    expect(mockProvider.refreshToken).toHaveBeenCalled();
    expect(retried).toBe(true);
    expect(result.data).toBe('retried-success');
    expect(error.config.headers.Authorization).toBe('Bearer refreshed-token');
  });

  it('should not retry if already retried', async () => {
    const mockProvider = {
        refreshToken: mock(async () => 'new'),
    } as any;

    const mockInstance: any = async () => {};
    mockInstance.interceptors = { request: { use: () => {} }, response: { use: () => {} } };

    let capturedErrorHandler: any;
    mockInstance.interceptors.response.use = (s: any, e: any) => { capturedErrorHandler = e; };

    setupAuthInterceptor(mockInstance, mockProvider);

    const error: any = new Error('401');
    error.response = { status: 401 };
    error.config = { _retry: true }; // Already retried

    try {
        await capturedErrorHandler(error);
        expect(true).toBe(false);
    } catch (e) {
        expect(e).toBe(error);
    }
    expect(mockProvider.refreshToken).not.toHaveBeenCalled();
  });
});
