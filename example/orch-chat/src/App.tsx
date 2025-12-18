import { useState } from 'react';
import './App.css';
import { Sidebar } from './components/Sidebar';
import { ChatWindow } from './components/ChatWindow';

function App() {
  const [activeChatId, setActiveChatId] = useState<string | null>(null);

  return (
    <div className="app-container">
      <Sidebar activeChatId={activeChatId} onSelectChat={setActiveChatId} />
      <div className="main-content">
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
