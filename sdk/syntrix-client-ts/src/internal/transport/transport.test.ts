import { describe, it, expect, mock } from 'bun:test';
import { RestTransport } from './rest-transport';

describe('RestTransport', () => {
  it('should return null on 404', async () => {
    const mockAxios = {
      get: mock(async () => {
        const err: any = new Error('Not Found');
        err.response = { status: 404 };
        throw err;
      }),
    } as any;

    const transport = new RestTransport(mockAxios);
    const result = await transport.get('/foo');
    expect(result).toBeNull();
  });

  it('should throw on other errors', async () => {
    const mockAxios = {
      get: mock(async () => {
        const err: any = new Error('Server Error');
        err.response = { status: 500 };
        throw err;
      }),
    } as any;

    const transport = new RestTransport(mockAxios);
    try {
        await transport.get('/foo');
        expect(true).toBe(false);
    } catch (e: any) {
        expect(e.response.status).toBe(500);
    }
  });

  it('should create resource', async () => {
    const mockAxios = {
      post: mock(async () => ({ data: { id: '123' } })),
    } as any;
    const transport = new RestTransport(mockAxios);
    const result = await transport.create('/foo', { bar: 'baz' });
    expect(result).toEqual({ id: '123' });
    expect(mockAxios.post).toHaveBeenCalledWith('/foo', { bar: 'baz' });
  });

  it('should set resource', async () => {
    const mockAxios = {
      put: mock(async () => ({ data: { id: '123' } })),
    } as any;
    const transport = new RestTransport(mockAxios);
    const result = await transport.set('/foo', { bar: 'baz' });
    expect(result).toEqual({ id: '123' });
    expect(mockAxios.put).toHaveBeenCalledWith('/foo', { bar: 'baz' });
  });

  it('should update resource', async () => {
    const mockAxios = {
      patch: mock(async () => ({ data: { id: '123' } })),
    } as any;
    const transport = new RestTransport(mockAxios);
    const result = await transport.update('/foo', { bar: 'baz' });
    expect(result).toEqual({ id: '123' });
    expect(mockAxios.patch).toHaveBeenCalledWith('/foo', { bar: 'baz' });
  });

  it('should delete resource', async () => {
    const mockAxios = {
      delete: mock(async () => ({})),
    } as any;
    const transport = new RestTransport(mockAxios);
    await transport.delete('/foo');
    expect(mockAxios.delete).toHaveBeenCalledWith('/foo');
  });

  it('should query resources', async () => {
    const mockAxios = {
      post: mock(async () => ({ data: { docs: [{ id: '1' }] } })),
    } as any;
    const transport = new RestTransport(mockAxios);
    const result = await transport.query('/query', {});
    expect(result).toEqual([{ id: '1' }]);
  });

  it('should query resources returning raw array', async () => {
    const mockAxios = {
      post: mock(async () => ({ data: [{ id: '1' }] })),
    } as any;
    const transport = new RestTransport(mockAxios);
    const result = await transport.query('/query', {});
    expect(result).toEqual([{ id: '1' }]);
  });
});
