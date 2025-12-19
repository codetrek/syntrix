import { useState } from 'react';
import { API_URL } from '../constants';
import { parseJwt } from '../utils';

interface SignInProps {
  onSignIn: (token: string, userId: string) => void;
}

export function SignIn({ onSignIn }: SignInProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const response = await fetch(`${API_URL}/v1/auth/login`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ username, password }),
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Login failed');
      }

      const data = await response.json();
      const accessToken = data.access_token;
      const refreshToken = data.refresh_token;

      if (!accessToken) {
        throw new Error('No access token received');
      }

      if (refreshToken) {
        localStorage.setItem('refresh_token', refreshToken);
      }

      const claims = parseJwt(accessToken);
      if (!claims || !claims.username) {
        throw new Error('Invalid token received');
      }

      onSignIn(accessToken, claims.username);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="signin-container">
      <form onSubmit={handleSubmit} className="signin-form">
        <h2>Sign In</h2>
        {error && <div className="error-message">{error}</div>}
        <div className="form-group">
          <label htmlFor="username">Username</label>
          <input
            id="username"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            placeholder="Enter username"
          />
        </div>
        <div className="form-group">
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            placeholder="Enter password"
          />
        </div>
        <button type="submit" disabled={loading}>
          {loading ? 'Signing in...' : 'Sign In'}
        </button>
        <p className="hint">
          Note: If the account does not exist, it will be automatically created.
        </p>
      </form>
    </div>
  );
}
