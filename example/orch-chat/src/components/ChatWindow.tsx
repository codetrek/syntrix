import React, { useEffect, useState, useRef } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { getMessagesCollection, getToolCallsCollection, getDatabase, startChatSync, startToolCallSync, Message, ToolCall, AgentTask } from '../db';
import { MessageInput } from './MessageInput';
import { TaskBlock } from './TaskBlock';

interface ChatWindowProps {
  chatId: string;
}

type ChatItem =
  | (Message & { type: 'message' })
  | (ToolCall & { type: 'toolcall' })
  | (Omit<AgentTask, 'type'> & { type: 'task'; taskType: AgentTask['type'] });

const ToolCallItem: React.FC<{ item: ToolCall }> = ({ item }) => {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <div className={`tool-call-box ${item.status}`}>
      <div className="tool-header" onClick={() => setIsOpen(!isOpen)} style={{ cursor: 'pointer' }}>
        <span className="tool-name">
          {isOpen ? '▼' : '▶'} 🔧 {item.toolName}
        </span>
        <span className={`tool-status ${item.status}`}>{item.status}</span>
      </div>
      {isOpen && (
        <div className="tool-body">
          <div className="tool-args">
            <pre>{JSON.stringify(item.args, null, 2)}</pre>
          </div>
          {item.result && (
            <div className="tool-result">
              <strong>Result:</strong>
              <pre>{item.result}</pre>
            </div>
          )}
          {item.error && (
            <div className="tool-error">
              <strong>Error:</strong> {item.error}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export const ChatWindow: React.FC<ChatWindowProps> = ({ chatId }) => {
  const [items, setItems] = useState<ChatItem[]>([]);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setItems([]); // Clear items immediately when chatId changes
    let isMounted = true;
    let msgSub: any;
    let toolSub: any;
    let taskSub: any;
    let msgSyncCancel: (() => void) | undefined;
    let toolSyncCancel: (() => void) | undefined;

    const init = async () => {
      const db = await getDatabase();
      const msgCollection = await getMessagesCollection(chatId);
      const toolCollection = await getToolCallsCollection(chatId);

      if (!isMounted) return;

      // Combine subscriptions
      let currentMessages: Message[] = [];
      let currentTools: ToolCall[] = [];
      let currentTasks: AgentTask[] = [];

      const updateItems = () => {
        if (!isMounted) return;

        // We need to interleave tasks.
        // Tasks have `triggerMessageId`. We should place them AFTER that message.
        // But for simplicity in sorting, we can use `createdAt`.
        // However, `createdAt` of a task is usually slightly after the user message.

        const combined: ChatItem[] = [
          ...currentMessages.map(m => ({ ...m, type: 'message' } as const)),
          ...currentTools.map(t => ({ ...t, type: 'toolcall' } as const)),
          ...currentTasks.map(t => {
            const { type, ...rest } = t;
            return { ...rest, type: 'task', taskType: type } as const;
          })
        ];
        combined.sort((a, b) => a.createdAt - b.createdAt);
        setItems(combined);
      };

      msgSub = msgCollection.find().sort({ createdAt: 'asc' }).$.subscribe(msgs => {
        currentMessages = msgs.map(m => m.toJSON());
        updateItems();
      });

      toolSub = toolCollection.find().sort({ createdAt: 'asc' }).$.subscribe(tools => {
        currentTools = tools.map(t => t.toJSON());
        updateItems();
      });

      taskSub = db.tasks.find({
          selector: { chatId },
          sort: [{ createdAt: 'asc' }]
      }).$.subscribe(tasks => {
          currentTasks = tasks.map(t => t.toJSON());
          updateItems();
      });

      // Start Syncs
      const { cancel: cancelMsg } = await startChatSync(chatId);
      msgSyncCancel = cancelMsg;

      const { cancel: cancelTool } = await startToolCallSync(chatId);
      toolSyncCancel = cancelTool;
    };

    init();

    return () => {
      isMounted = false;
      if (msgSub) msgSub.unsubscribe();
      if (toolSub) toolSub.unsubscribe();
      if (taskSub) taskSub.unsubscribe();
      if (msgSyncCancel) msgSyncCancel();
      if (toolSyncCancel) toolSyncCancel();
    };
  }, [chatId]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [items]);

  return (
    <div className="chat-window">
      <div className="messages">
        {items.map(item => {
          if (item.type === 'message') {
            return (
              <div key={item.id} className={`message ${item.role}`}>
                <div className="content">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>
                    {item.content}
                  </ReactMarkdown>
                </div>
              </div>
            );
          } else if (item.type === 'task') {
              const { type, taskType, ...rest } = item;
              const originalTask: AgentTask = { ...rest, type: taskType };
              return <TaskBlock key={item.id} task={originalTask} chatId={chatId} />;
          } else {
            return <ToolCallItem key={item.id} item={item} />;
          }
        })}
        <div ref={messagesEndRef} />
      </div>
      <MessageInput chatId={chatId} />
    </div>
  );
};
