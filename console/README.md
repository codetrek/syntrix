# Syntrix Console

This directory contains the frontend console for Syntrix.

## Development

The console is a simple single-page application (SPA) contained in `index.html`. It uses vanilla JavaScript and CSS to minimize dependencies.

## Serving

The console is served by the Syntrix API server at `/console/`.

## Features

- **Login/Logout**: Authenticate using Syntrix Auth Service.
- **Dashboard**: View and manage user data.
- **Profile**: View user profile.
- **Admin**: (Admin only) Manage users and rules.

## API Integration

The console communicates with the Syntrix API at `/api/v1`.
