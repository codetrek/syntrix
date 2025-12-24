export class Outbox {
    async push(mutation: any): Promise<void> {}
    async pull(): Promise<any[]> { return []; }
}
