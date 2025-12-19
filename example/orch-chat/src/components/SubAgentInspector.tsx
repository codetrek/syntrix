import React, { useEffect, useState, useRef } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { getDatabase, startSubAgentSync, SubAgentMessage, SubAgentToolCall } from '../db';

interface SubAgentInspectorProps {
  chatId: string;
  subAgentId: string;
  onClose: () => void;
}

type InspectorItem =
  | (SubAgentMessage & { type: 'message' })
  | (SubAgentToolCall & { type: 'toolcall' });

const ToolCallItem: React.FC<{ item: SubAgentToolCall }> = ({ item }) => {
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

export const SubAgentInspector: React.FC<SubAgentInspectorProps> = ({ chatId, subAgentId, onClose }) => {
  const [items, setItems] = useState<InspectorItem[]>([]);
  const [agentStatus, setAgentStatus] = useState<string>('active');
  const messagesEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let isMounted = true;
    let msgSub: any;
    let toolSub: any;
    let agentSub: any;
    let syncCancel: (() => void) | undefined;

    const init = async () => {
      const db = await getDatabase();

      if (!isMounted) return;

      // 1. Subscribe to Agent Status
      agentSub = db.sub_agents.findOne(subAgentId).$.subscribe(doc => {
          if (doc) {
              setAgentStatus(doc.status);
          }
      });

      // 2. Subscribe to Messages & Tools
      let currentMessages: SubAgentMessage[] = [];
      let currentTools: SubAgentToolCall[] = [];

      const updateItems = () => {
        if (!isMounted) return;
        const combined: InspectorItem[] = [
          ...currentMessages
            .filter(m => m.role !== 'tool') // Hide tool outputs to avoid redundancy with ToolCallItem
            .map(m => ({ ...m, type: 'message' } as const)),
          ...currentTools.map(t => ({ ...t, type: 'toolcall' } as const))
        ];
        combined.sort((a, b) => a.createdAt - b.createdAt);
        setItems(combined);
      };

      msgSub = db.sub_agent_messages.find({
          selector: { subAgentId },
          sort: [{ createdAt: 'asc' }]
      }).$.subscribe(msgs => {
        currentMessages = msgs.map(m => JSON.parse(JSON.stringify(m.toJSON())));
        updateItems();
      });

      toolSub = db.sub_agent_tool_calls.find({
          selector: { subAgentId },
          sort: [{ createdAt: 'asc' }]
      }).$.subscribe(tools => {
        currentTools = tools.map(t => t.toJSON());
        updateItems();
      });

      // 3. Start Sync
      const { cancel } = await startSubAgentSync(chatId, subAgentId);
      syncCancel = cancel;
    };

    init();

    return () => {
      isMounted = false;
      if (msgSub) msgSub.unsubscribe();
      if (toolSub) toolSub.unsubscribe();
      if (agentSub) agentSub.unsubscribe();
      if (syncCancel) syncCancel();
    };
  }, [chatId, subAgentId]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [items]);

  const isInteractive = agentStatus === 'waiting_for_user';

  return (
    <div className="sub-agent-inspector-overlay">
      <div className="sub-agent-inspector">
        <div className="inspector-header">
            <h3>Sub-Agent Inspector ({agentStatus})</h3>
            <button onClick={onClose}>Close</button>
        </div>
        <div className="messages">
          {items.map(item => {
            if (item.type === 'message') {
              return (
                <div key={item.id} className={`message ${item.role}`}>
                  <div className="role-label">{item.role}</div>
                  <div className="content">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {item.content}
                    </ReactMarkdown>
                  </div>
                </div>
              );
            } else {
              return <ToolCallItem key={item.id} item={item} />;
            }
          })}
          <div ref={messagesEndRef} />
        </div>

        {/* Read Only Status Bar */}
        <div className="inspector-status-bar">
            {isInteractive ? (
                <span style={{ color: 'orange', fontWeight: 'bold' }}>
                    ⚠️ Agent is waiting for input. Please reply in the main chat.
                </span>
            ) : (
                <span>Agent is thinking... (Read Only)</span>
            )}
        </div>
      </div>
    </div>
  );
};
