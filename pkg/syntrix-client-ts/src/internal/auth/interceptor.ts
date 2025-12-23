import { AxiosInstance, AxiosError, InternalAxiosRequestConfig } from 'axios';
import { TokenProvider } from './types';

export function setupAuthInterceptor(axiosInstance: AxiosInstance, provider: TokenProvider) {
  axiosInstance.interceptors.request.use(async (config) => {
    const token = await provider.getToken();
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  });

  axiosInstance.interceptors.response.use(
    (response) => response,
    async (error: AxiosError) => {
      const config = error.config as InternalAxiosRequestConfig & { _retry?: boolean };

      if (!config || !error.response) {
        return Promise.reject(error);
      }

      if ((error.response.status === 401 || error.response.status === 403) && !config._retry) {
        config._retry = true;
        try {
          const newToken = await provider.refreshToken();
          config.headers.Authorization = `Bearer ${newToken}`;
          return axiosInstance(config);
        } catch (refreshError) {
          return Promise.reject(refreshError);
        }
      }

      return Promise.reject(error);
    }
  );
}
