import { CheckpointManager } from './checkpoint';
import { Outbox } from './outbox';
import { RealtimeListener } from './realtime';
import { Puller } from './pull';
import { Pusher } from './push';

export class ReplicationCoordinator {
    constructor(
        private checkpoint: CheckpointManager,
        private outbox: Outbox,
        private realtime: RealtimeListener,
        private puller: Puller,
        private pusher: Pusher
    ) {}

    start() {
        this.realtime.connect();
        this.realtime.onEvent(() => {
            this.schedulePull();
        });
        // Initial sync
        this.schedulePull();
        this.schedulePush();
    }

    stop() {
        this.realtime.disconnect();
    }

    private async schedulePull() {
        const cp = await this.checkpoint.getLastCheckpoint();
        await this.puller.pullChanges(cp);
    }

    private async schedulePush() {
        const changes = await this.outbox.pull();
        if (changes.length > 0) {
            await this.pusher.pushChanges(changes);
        }
    }
}
