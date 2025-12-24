export class CheckpointManager {
    async getLastCheckpoint(): Promise<string | null> { return null; }
    async saveCheckpoint(checkpoint: string): Promise<void> {}
}
