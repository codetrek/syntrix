import { AxiosInstance, AxiosError, InternalAxiosRequestConfig } from 'axios';
import { TokenProvider } from './types';
import { AuthSessionChangedError, SyntrixError } from '../../api/errors';

type AuthRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
  _syntrixAuthSessionVersion?: number;
};

export function setupAuthInterceptor(axiosInstance: AxiosInstance, provider: TokenProvider) {
  const assertSession = (version: number | undefined) => {
    if (version !== provider.getSessionVersion()) throw new AuthSessionChangedError();
  };

  axiosInstance.interceptors.request.use(async (config) => {
    const authConfig = config as AuthRequestConfig;
    // Axios copies this field into retries; never bind an old request to a new session.
    authConfig._syntrixAuthSessionVersion ??= provider.getSessionVersion();
    assertSession(authConfig._syntrixAuthSessionVersion);
    let token: string | null;
    try {
      token = await provider.getToken();
    } finally {
      assertSession(authConfig._syntrixAuthSessionVersion);
    }
    if (token) {
      config.headers.set('Authorization', `Bearer ${token}`);
    } else {
      config.headers.delete('Authorization');
    }
    return config;
  });

  axiosInstance.interceptors.response.use(
    (response) => response,
    async (error: AxiosError) => {
      const config = error.config as AuthRequestConfig;

      if (!config || !error.response) {
        return Promise.reject(error);
      }

      const status = error.response.status;
      const data = error.response.data as any;

      if (status === 401 || status === 403) {
        assertSession(config._syntrixAuthSessionVersion);
      }

      // Handle rate limiting (429)
      if (status === 429) {
        const retryAfterHeader = error.response.headers['retry-after'];
        const retryAfter = retryAfterHeader ? parseInt(retryAfterHeader, 10) : 60;
        const syntrixError = SyntrixError.fromResponse(status, data, retryAfter);
        return Promise.reject(syntrixError);
      }

      // Handle auth errors with token refresh
      if ((status === 401 || status === 403) && !config._retry) {
        config._retry = true;
        try {
          const newToken = await provider.refreshToken();
          assertSession(config._syntrixAuthSessionVersion);
          config.headers.set('Authorization', `Bearer ${newToken}`);
          return axiosInstance(config);
        } catch (refreshError) {
          assertSession(config._syntrixAuthSessionVersion);
          // Convert to SyntrixError for consistent error handling
          if (refreshError instanceof AuthSessionChangedError || refreshError instanceof SyntrixError) {
            return Promise.reject(refreshError);
          }
          return Promise.reject(SyntrixError.fromResponse(401, { code: 'UNAUTHORIZED', message: 'Authentication failed' }));
        }
      }

      // Convert all API errors to SyntrixError
      if (data && (data.code || data.message || data.errors)) {
        return Promise.reject(SyntrixError.fromResponse(status, data));
      }

      return Promise.reject(error);
    }
  );
}
