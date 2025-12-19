import { useState, useEffect } from 'react';
import './App.css';
import { Sidebar } from './components/Sidebar';
import { ChatWindow } from './components/ChatWindow';
import { SignIn } from './components/SignIn';
import { setAuth, logout, checkAuth } from './auth';

function App() {
  const [activeChatId, setActiveChatId] = useState<string | null>(null);
  const [isAuthenticated, setIsAuthenticated] = useState(false);

  useEffect(() => {
    const initAuth = async () => {
      const success = await checkAuth();
      if (success) {
        setIsAuthenticated(true);
      }
    };

    initAuth();
  }, []);

  const handleSignIn = (token: string, userId: string) => {
    setAuth(token, userId);
    setIsAuthenticated(true);
  };

  const handleSignOut = async () => {
    await logout();
    setIsAuthenticated(false);
    setActiveChatId(null);
  };

  if (!isAuthenticated) {
    return <SignIn onSignIn={handleSignIn} />;
  }

  return (
    <div className="app-container">
      <Sidebar activeChatId={activeChatId} onSelectChat={setActiveChatId} />
      <div className="main-content">
        <div className="header-bar">
            <button onClick={handleSignOut} className="signout-btn">Sign Out</button>
        </div>
        {activeChatId ? (
          <ChatWindow chatId={activeChatId} />
        ) : (
          <div className="empty-state">Select a chat to start messaging</div>
        )}
      </div>
    </div>
  );
}

export default App;
