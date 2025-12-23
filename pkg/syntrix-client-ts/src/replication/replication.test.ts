import { describe, it, expect, mock } from 'bun:test';
import { ReplicationCoordinator } from './coordinator';
import { CheckpointManager } from './checkpoint';
import { Outbox } from './outbox';
import { RealtimeListener } from './realtime';
import { Puller } from './pull';
import { Pusher } from './push';

describe('Replication Scaffolding', () => {
  it('should start and stop coordinator', () => {
    const checkpoint = new CheckpointManager();
    const outbox = new Outbox();
    const realtime = new RealtimeListener();
    const puller = new Puller();
    const pusher = new Pusher();

    // Mock realtime methods
    realtime.connect = mock(() => {});
    realtime.disconnect = mock(() => {});
    realtime.onEvent = mock((cb) => {
        // Simulate an event to trigger schedulePull
        cb({});
    });

    const coordinator = new ReplicationCoordinator(
        checkpoint, outbox, realtime, puller, pusher
    );

    coordinator.start();
    expect(realtime.connect).toHaveBeenCalled();
    expect(realtime.onEvent).toHaveBeenCalled();

    coordinator.stop();
    expect(realtime.disconnect).toHaveBeenCalled();
  });

  it('should have basic methods on components', async () => {
    const checkpoint = new CheckpointManager();
    expect(await checkpoint.getLastCheckpoint()).toBeNull();
    await checkpoint.saveCheckpoint('abc');

    const outbox = new Outbox();
    await outbox.push({});
    expect(await outbox.pull()).toEqual([]);

    const puller = new Puller();
    expect(await puller.pullChanges(null)).toEqual({});

    const pusher = new Pusher();
    await pusher.pushChanges([]);

    const realtime = new RealtimeListener();
    realtime.connect();
    realtime.disconnect();
    realtime.onEvent(() => {});
  });
});
