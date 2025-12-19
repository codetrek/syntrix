import { API_URL } from './constants';
import { parseJwt } from './utils';

let authToken: string | null = null;
let currentUserId: string | null = null;
let logoutHandlers: (() => Promise<void>)[] = [];

export const getAuthToken = () => authToken;
export const getUserId = () => currentUserId;

export const onLogout = (handler: () => Promise<void>) => {
    logoutHandlers.push(handler);
};

export const setAuth = (token: string, userId: string) => {
    authToken = token;
    currentUserId = userId;
};

export const logout = async () => {
    for (const handler of logoutHandlers) {
        await handler();
    }
    authToken = null;
    currentUserId = null;
    localStorage.removeItem('refresh_token');
    window.location.reload();
};

let checkAuthPromise: Promise<boolean> | null = null;

export const checkAuth = async (): Promise<boolean> => {
    if (checkAuthPromise) return checkAuthPromise;

    checkAuthPromise = (async () => {
        const refreshToken = localStorage.getItem('refresh_token');
        if (!refreshToken) return false;

        try {
            const response = await fetch(`${API_URL}/v1/auth/refresh`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ refresh_token: refreshToken }),
            });

            if (response.ok) {
                const data = await response.json();
                const accessToken = data.access_token;
                const newRefreshToken = data.refresh_token;

                if (accessToken) {
                    const claims = parseJwt(accessToken);
                    if (claims && claims.username) {
                        setAuth(accessToken, claims.username);
                        if (newRefreshToken) {
                            localStorage.setItem('refresh_token', newRefreshToken);
                        }
                        return true;
                    }
                }
            } else {
                // Refresh failed, clear token
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

// --- Auth Helper ---

let isRefreshing = false;
let refreshSubscribers: ((token: string) => void)[] = [];

const onRefreshed = (token: string) => {
    refreshSubscribers.map(cb => cb(token));
    refreshSubscribers = [];
};

const addRefreshSubscriber = (cb: (token: string) => void) => {
    refreshSubscribers.push(cb);
};

export const fetchWithAuth = async (url: string, options: RequestInit = {}): Promise<Response> => {
    const headers: any = { ...options.headers };
    if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`;
    }

    const response = await fetch(url, { ...options, headers });

    if (response.status === 401) {
        if (!localStorage.getItem('refresh_token')) {
            await logout();
            throw new Error('Session expired');
        }

        if (isRefreshing) {
            return new Promise((resolve) => {
                addRefreshSubscriber(async (token) => {
                    headers['Authorization'] = `Bearer ${token}`;
                    resolve(await fetch(url, { ...options, headers }));
                });
            });
        }

        isRefreshing = true;
        try {
            const refreshResponse = await fetch(`${API_URL}/v1/auth/refresh`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ refresh_token: localStorage.getItem('refresh_token') })
            });

            if (refreshResponse.ok) {
                const data = await refreshResponse.json();
                authToken = data.access_token;
                if (data.refresh_token) {
                    localStorage.setItem('refresh_token', data.refresh_token);
                }
                isRefreshing = false;
                onRefreshed(authToken!);

                // Retry original request
                headers['Authorization'] = `Bearer ${authToken}`;
                return fetch(url, { ...options, headers });
            } else {
                isRefreshing = false;
                await logout();
                throw new Error('Session expired');
            }
        } catch (e) {
            isRefreshing = false;
            await logout();
            throw e;
        }
    }

    return response;
};
