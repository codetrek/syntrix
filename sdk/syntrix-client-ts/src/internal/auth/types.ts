export interface AuthConfig {
  token?: string;
  refreshToken?: string;
  refreshUrl?: string;
  onTokenRefresh?: (newToken: string) => void;
  onAuthError?: (error: Error) => void;
}

export interface TokenProvider {
  getToken(): Promise<string | null>;
  setToken(token: string): void;
  refreshToken(): Promise<string>;
}
