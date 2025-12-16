import { useState, useEffect, useRef } from 'react';
import { getDatabase, MyDatabase, Message, COLLECTION_NAME } from './db';
import { v4 as uuidv4 } from 'uuid';
import './App.css';

const SENDER_ID = 'user-' + Math.floor(Math.random() * 1000);

function App() {
  const [db, setDb] = useState<MyDatabase | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [inputText, setInputText] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  useEffect(() => {
    scrollToBottom();
  }, [messages]);

  useEffect(() => {
    const initDb = async () => {
      const database = await getDatabase();
      setDb(database);

      // Subscribe to query
      database.messages.find({
        sort: [{ timestamp: 'asc' }]
      }).$.subscribe(docs => {
        setMessages(docs.map(doc => doc.toJSON() as Message));
      });
    };
    initDb();
  }, []);

  const handleSend = async () => {
    if (!db || !inputText.trim()) return;

    const newMessage: Message = {
      id: uuidv4(),
      text: inputText,
      sender: SENDER_ID,
      timestamp: Date.now()
    };

    await db.messages.insert(newMessage);
    setInputText('');
  };

  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handleSend();
    }
  };

  if (!db) return <div>Loading Database...</div>;

  return (
    <div className="chat-container">
      <div className="messages-list">
        {messages.map(msg => (
          <div key={msg.id} className={`message ${msg.sender === SENDER_ID ? 'own' : ''}`}>
            <div className="message-header">{msg.sender}</div>
            <div className="message-content">{msg.text}</div>
          </div>
        ))}
        <div ref={messagesEndRef} />
      </div>
      <div className="input-area">
        <input
          type="text"
          value={inputText}
          onChange={(e) => setInputText(e.target.value)}
          onKeyDown={handleKeyPress}
          placeholder="Type a message..."
        />
        <button onClick={handleSend}>Send</button>
      </div>
    </div>
  );
}

export default App;
