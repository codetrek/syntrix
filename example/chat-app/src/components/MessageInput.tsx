import React, { useState } from 'react';
import { syncMessages } from '../db';
import { v4 as uuidv4 } from 'uuid';

interface MessageInputProps {
  chatId: string;
}

export const MessageInput: React.FC<MessageInputProps> = ({ chatId }) => {
  const [text, setText] = useState('');

  const sendMessage = async () => {
    if (!text.trim()) return;
    const collection = await syncMessages(chatId);
    await collection.insert({
      id: uuidv4(),
      role: 'user',
      content: text,
      createdAt: Date.now()
    });
    setText('');
  };

  return (
    <div className="input-area">
      <input
        value={text}
        onChange={e => setText(e.target.value)}
        onKeyDown={e => e.key === 'Enter' && sendMessage()}
        placeholder="Type a message..."
      />
      <button onClick={sendMessage}>Send</button>
    </div>
  );
};
