import React, { useEffect, useState, useRef } from 'react';
import { syncMessages, Message } from '../db';
import { MessageInput } from './MessageInput';

interface ChatWindowProps {
  chatId: string;
}

export const ChatWindow: React.FC<ChatWindowProps> = ({ chatId }) => {
  const [messages, setMessages] = useState<Message[]>([]);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let sub: any;
    syncMessages(chatId).then(collection => {
      sub = collection.find().sort({ createdAt: 'asc' }).$.subscribe(msgs => {
        setMessages(msgs.map(m => m.toJSON()));
      });
    });

    return () => {
      if (sub) sub.unsubscribe();
    };
  }, [chatId]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  return (
    <div className="chat-window">
      <div className="messages">
        {messages.map(msg => (
          <div key={msg.id} className={`message ${msg.role}`}>
            <div className="content">{msg.content}</div>
          </div>
        ))}
        <div ref={messagesEndRef} />
      </div>
      <MessageInput chatId={chatId} />
    </div>
  );
};
