import React, { useEffect, useState } from 'react';
import { getDatabase, AgentTask } from '../db';
import { SubAgentInspector } from './SubAgentInspector';

interface TaskBlockProps {
  task: AgentTask;
  chatId: string;
}

export const TaskBlock: React.FC<TaskBlockProps> = ({ task, chatId }) => {
  const [showInspector, setShowInspector] = useState(false);
  const [subAgentStatus, setSubAgentStatus] = useState<string>(task.status);

  useEffect(() => {
      // Subscribe to task updates to keep status fresh
      const init = async () => {
          const db = await getDatabase();
          const sub = db.tasks.findOne(task.id).$.subscribe(doc => {
              if (doc) {
                  setSubAgentStatus(doc.status);
              }
          });
          return () => sub.unsubscribe();
      };
      init();
  }, [task.id]);

  return (
    <>
      <div className={`task-block ${subAgentStatus}`}>
        <div className="task-icon">
            {task.name.includes('researcher') ? '🔍' : '🤖'}
        </div>
        <div className="task-info">
            <div className="task-name">{task.name}</div>
            <div className="task-status">Status: {subAgentStatus}</div>
            {task.result && <div className="task-result-preview">{task.result.substring(0, 50)}...</div>}
        </div>
        <div className="task-actions">
            <button onClick={() => setShowInspector(true)}>
                {subAgentStatus === 'waiting' ? '🔴 View Request' : 'View Details'}
            </button>
        </div>
      </div>

      {showInspector && task.subAgentId && (
          <SubAgentInspector
            chatId={chatId}
            subAgentId={task.subAgentId}
            onClose={() => setShowInspector(false)}
          />
      )}
    </>
  );
};
