import axios from 'axios';
import { AuthConfig, TokenProvider } from './types';

export class DefaultTokenProvider implements TokenProvider {
  private token: string | null = null;
  private refreshPromise: Promise<string> | null = null;

  constructor(private config: AuthConfig) {
    this.token = config.token || null;
  }

  async getToken(): Promise<string | null> {
    return this.token;
  }

  setToken(token: string): void {
    this.token = token;
  }

  async refreshToken(): Promise<string> {
    if (!this.config.refreshUrl) {
      throw new Error('Refresh URL not configured');
    }

    if (this.refreshPromise) {
      return this.refreshPromise;
    }

    this.refreshPromise = this.performRefresh();

    try {
      return await this.refreshPromise;
    } finally {
      this.refreshPromise = null;
    }
  }

  private async performRefresh(): Promise<string> {
    try {
      const response = await axios.post(this.config.refreshUrl!, {
        refreshToken: this.config.refreshToken
      });

      const newToken = response.data.token;
      if (!newToken) {
          throw new Error('Invalid refresh response: missing token');
      }

      this.token = newToken;
      this.config.onTokenRefresh?.(newToken);
      return newToken;
    } catch (error) {
      this.config.onAuthError?.(error as Error);
      throw error;
    }
  }
}
