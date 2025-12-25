import { API_URL } from './constants';
import { parseJwt } from './utils';
import { getSyntrixClient, resetSyntrixClient } from './client';

let authToken: string | null = null;
let currentUserId: string | null = null;
let logoutHandlers: (() => Promise<void>)[] = [];

export const getAuthToken = () => authToken;
export const getUserId = () => currentUserId;

export const onLogout = (handler: () => Promise<void>) => {
    logoutHandlers.push(handler);
};

export const setAuth = (token: string, userId: string, refreshToken?: string) => {
    authToken = token;
    currentUserId = userId;
    if (token) {
        localStorage.setItem('access_token', token);
    }
    if (refreshToken) {
        localStorage.setItem('refresh_token', refreshToken);
    }
    resetSyntrixClient(token, refreshToken || localStorage.getItem('refresh_token'));
};

export const logout = async () => {
    for (const handler of logoutHandlers) {
        await handler();
    }
    authToken = null;
    currentUserId = null;
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
    resetSyntrixClient(null, null);
    window.location.reload();
};

let checkAuthPromise: Promise<boolean> | null = null;

export const checkAuth = async (): Promise<boolean> => {
    if (checkAuthPromise) return checkAuthPromise;

    checkAuthPromise = (async () => {
        const accessToken = localStorage.getItem('access_token');
        const refreshToken = localStorage.getItem('refresh_token');
        if (!refreshToken && !accessToken) return false;

        try {
            if (accessToken) {
                const claims = parseJwt(accessToken);
                if (claims && claims.username) {
                    setAuth(accessToken, claims.username, refreshToken || undefined);
                    return true;
                }
            }

            if (!refreshToken) return false;

            const response = await fetch(`${API_URL}/auth/v1/refresh`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ refresh_token: refreshToken }),
            });

            if (response.ok) {
                const data = await response.json();
                const newAccessToken = data.access_token;
                const newRefreshToken = data.refresh_token;

                if (newAccessToken) {
                    const claims = parseJwt(newAccessToken);
                    if (claims && claims.username) {
                        setAuth(newAccessToken, claims.username, newRefreshToken || refreshToken);
                        return true;
                    }
                }
            } else {
                localStorage.removeItem('refresh_token');
            }
        } catch (error) {
            console.error('Auth check failed:', error);
            localStorage.removeItem('refresh_token');
        }
        return false;
    })();

    try {
        return await checkAuthPromise;
    } finally {
        checkAuthPromise = null;
    }
};

export const loginWithSdk = async (username: string, password: string) => {
    const client = getSyntrixClient();
    const result = await client.login(username, password);
    const accessToken = result.access_token;
    const refreshToken = result.refresh_token;
    const claims = parseJwt(accessToken);
    if (!claims || !claims.username) {
        throw new Error('Invalid token received');
    }
    setAuth(accessToken, claims.username, refreshToken);
    return { accessToken, userId: claims.username };
};
