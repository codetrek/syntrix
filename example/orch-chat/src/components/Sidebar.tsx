import React, { useEffect, useState } from 'react';
import { getDatabase, Chat } from '../db';
import { v4 as uuidv4 } from 'uuid';

interface SidebarProps {
  activeChatId: string | null;
  onSelectChat: (chatId: string) => void;
}

export const Sidebar: React.FC<SidebarProps> = ({ activeChatId, onSelectChat }) => {
  const [chats, setChats] = useState<Chat[]>([]);

  useEffect(() => {
    getDatabase().then(db => {
      db.chats.find().sort({ updatedAt: 'desc' }).$.subscribe(chats => {
        setChats(chats.map(c => c.toJSON()));
      });
    });
  }, []);

  const createNewChat = async () => {
    const db = await getDatabase();
    const id = uuidv4();
    await db.chats.insert({
      id,
      title: 'New Chat',
      updatedAt: Date.now()
    });
    onSelectChat(id);
  };

  return (
    <div className="sidebar">
      <button onClick={createNewChat}>+ New Chat</button>
      <div className="chat-list">
        {chats.map(chat => (
          <div
            key={chat.id}
            className={`chat-item ${activeChatId === chat.id ? 'active' : ''}`}
            onClick={() => onSelectChat(chat.id)}
          >
            {chat.title}
          </div>
        ))}
      </div>
    </div>
  );
};
