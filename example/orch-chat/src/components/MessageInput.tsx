import React, { useState } from 'react';
import { syncMessages } from '../db';
import { generateShortId } from '../utils';

interface MessageInputProps {
  chatId: string;
  customSubmit?: (text: string) => Promise<void>;
}

export const MessageInput: React.FC<MessageInputProps> = ({ chatId, customSubmit }) => {
  const [text, setText] = useState('');

  const sendMessage = async () => {
    if (!text.trim()) return;

    if (customSubmit) {
        await customSubmit(text);
    } else {
        const collection = await syncMessages(chatId);
        await collection.insert({
          id: generateShortId(),
          role: 'user',
          content: text,
          createdAt: Date.now()
        });
    }
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
