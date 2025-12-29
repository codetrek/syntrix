import axios from 'axios';
import { AuthConfig, TokenProvider, LoginResponse, AuthService } from './types';

export class DefaultTokenProvider implements TokenProvider, AuthService {
  private token: string | null = null;
  private _refreshToken: string | null = null;
  private refreshPromise: Promise<string> | null = null;
  private baseUrl: string;

  constructor(private config: AuthConfig, baseUrl?: string) {
    this.token = config.token || null;
    this._refreshToken = config.refreshToken || null;
    this.baseUrl = baseUrl || '';
  }

  async getToken(): Promise<string | null> {
    return this.token;
  }

  setToken(token: string): void {
    this.token = token;
  }

  setRefreshToken(token: string): void {
    this._refreshToken = token;
  }

  isAuthenticated(): boolean {
    return this.token !== null;
  }

  async login(username: string, password: string, tenantId?: string): Promise<LoginResponse> {
    const url = this.config.refreshUrl?.replace('/refresh', '/login') || `${this.baseUrl}/auth/v1/login`;
    const response = await axios.post<LoginResponse>(url, { username, password, tenant: tenantId || this.config.tenantId });

    this.token = response.data.access_token;
    this._refreshToken = response.data.refresh_token;

    return response.data;
  }

  async logout(): Promise<void> {
    if (this._refreshToken) {
      const url = this.config.refreshUrl?.replace('/refresh', '/logout') || `${this.baseUrl}/auth/v1/logout`;
      try {
        await axios.post(url, { refresh_token: this._refreshToken });
      } catch {
        // Ignore logout errors
      }
    }
    this.token = null;
    this._refreshToken = null;
  }

  async refreshToken(): Promise<string> {
    if (!this._refreshToken) {
      throw new Error('No refresh token available');
    }

    const refreshUrl = this.config.refreshUrl || `${this.baseUrl}/auth/v1/refresh`;

    if (this.refreshPromise) {
      return this.refreshPromise;
    }

    this.refreshPromise = this.performRefresh(refreshUrl);

    try {
      return await this.refreshPromise;
    } finally {
      this.refreshPromise = null;
    }
  }

  private async performRefresh(refreshUrl: string): Promise<string> {
    try {
      const response = await axios.post(refreshUrl, {
        refresh_token: this._refreshToken
      });

      const newToken = response.data.access_token;
      const newRefreshToken = response.data.refresh_token;

      if (!newToken) {
        throw new Error('Invalid refresh response: missing token');
      }

      this.token = newToken;
      if (newRefreshToken) {
        this._refreshToken = newRefreshToken;
      }

      this.config.onTokenRefresh?.(newToken);
      return newToken;
    } catch (error) {
      this.config.onAuthError?.(error as Error);
      throw error;
    }
  }
}
