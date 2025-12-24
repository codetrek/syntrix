import { describe, it, expect, mock } from 'bun:test';
import { ReplicationCoordinator } from './coordinator';
import { CheckpointManager } from './checkpoint';
import { Outbox } from './outbox';
import { RealtimeListener } from './realtime';
import { Puller } from './pull';
import { Pusher } from './push';
import { SyntrixClient } from '../clients/syntrix-client';
import axios from 'axios';

describe('Replication Full Flow', () => {
  it('should sync data via push/pull and react to realtime events triggered by REST', async () => {
    // 1. Setup Mocks
    const checkpoint = new CheckpointManager();
    const outbox = new Outbox();
    const realtime = new RealtimeListener();
    const puller = new Puller();
    const pusher = new Pusher();

    // Mock internal behaviors
    checkpoint.getLastCheckpoint = mock(async () => 'cp-1');
    outbox.pull = mock(async () => [{ id: 'local-change' }]); // Simulate pending local change
    pusher.pushChanges = mock(async () => {});
    puller.pullChanges = mock(async () => ({ docs: [] }));

    let realtimeCallback: (event: any) => void = () => {};
    realtime.connect = mock(() => {});
    realtime.onEvent = mock((cb) => { realtimeCallback = cb; });
    realtime.disconnect = mock(() => {});

    // 2. Initialize Coordinator
    const coordinator = new ReplicationCoordinator(
        checkpoint, outbox, realtime, puller, pusher
    );

    // 3. Start Replication (Should trigger initial Push/Pull)
    coordinator.start();

    // Verify initial connection and sync
    expect(realtime.connect).toHaveBeenCalled();
    // Wait for async promises to settle (start calls async methods without awaiting)
    await new Promise(resolve => setTimeout(resolve, 10));

    expect(checkpoint.getLastCheckpoint).toHaveBeenCalled();
    expect(puller.pullChanges).toHaveBeenCalledWith('cp-1');
    expect(outbox.pull).toHaveBeenCalled();
    expect(pusher.pushChanges).toHaveBeenCalled();

    // 4. Simulate rest modification
    // We mock the REST client to simulate a successful write
    const mockAxios = {
        post: mock(async () => ({ data: { id: 'doc-1' } })),
        interceptors: { request: { use: () => {} }, response: { use: () => {} } }
    };
    const originalCreate = axios.create;
    axios.create = mock(() => mockAxios as any) as any;

    const client = new SyntrixClient('http://localhost', {});
    await client.collection('users').add({ name: 'New User' });

    expect(mockAxios.post).toHaveBeenCalled();

    // 5. Simulate Realtime Notification (Server reaction to REST write)
    // In a real system, the server sends this. Here we manually fire it.
    realtimeCallback({ type: 'update', path: 'users/doc-1' });

    // 6. Verify Reaction (Pull triggered again)
    await new Promise(resolve => setTimeout(resolve, 10));

    // pullChanges should have been called twice: once at start, once after event
    expect(puller.pullChanges).toHaveBeenCalledTimes(2);

    // Cleanup
    coordinator.stop();
    axios.create = originalCreate;
  });
});
