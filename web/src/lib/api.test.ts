import { describe, it, expect, vi, beforeEach } from 'vitest';

const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

import { api } from './api';

beforeEach(() => {
  mockFetch.mockReset();
});

function mockJsonResponse(data: unknown, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(data),
    text: () => Promise.resolve(JSON.stringify(data)),
  });
}

describe('api.devices', () => {
  it('returns device list on success', async () => {
    const mockDevices = [
      { id: '1', name: 'device-1', status: 'online', labels: {} },
      { id: '2', name: 'device-2', status: 'offline', labels: {} },
    ];
    mockFetch.mockReturnValue(mockJsonResponse(mockDevices));

    const result = await api.devices();

    expect(mockFetch).toHaveBeenCalledWith('/api/devices');
    expect(result).toEqual(mockDevices);
  });

  it('throws on HTTP error', async () => {
    mockFetch.mockReturnValue(mockJsonResponse('not found', 404));

    await expect(api.devices()).rejects.toThrow('404');
  });
});

describe('api.device', () => {
  it('fetches single device by id', async () => {
    const mockDevice = { id: 'abc', name: 'device-1', status: 'online' };
    mockFetch.mockReturnValue(mockJsonResponse(mockDevice));

    const result = await api.device('abc');

    expect(mockFetch).toHaveBeenCalledWith('/api/devices/abc');
    expect(result).toEqual(mockDevice);
  });
});

describe('api.deviceTelemetry', () => {
  it('fetches telemetry with limit', async () => {
    const mockPoints = [{ tps: 28, ttft_ms: 120, mem_mb: 3800 }];
    mockFetch.mockReturnValue(mockJsonResponse(mockPoints));

    const result = await api.deviceTelemetry('abc', 30);

    expect(mockFetch).toHaveBeenCalledWith('/api/devices/abc/telemetry?limit=30');
    expect(result).toEqual(mockPoints);
  });

  it('uses default limit of 60', async () => {
    mockFetch.mockReturnValue(mockJsonResponse([]));

    await api.deviceTelemetry('abc');

    expect(mockFetch).toHaveBeenCalledWith('/api/devices/abc/telemetry?limit=60');
  });
});

describe('api.deployments', () => {
  it('returns deployment list', async () => {
    const mockDeps = [{ id: '1', model_id: 'gemma', status: 'completed' }];
    mockFetch.mockReturnValue(mockJsonResponse(mockDeps));

    const result = await api.deployments();

    expect(mockFetch).toHaveBeenCalledWith('/api/deployments');
    expect(result).toEqual(mockDeps);
  });
});

describe('api.artifacts', () => {
  it('returns artifact list', async () => {
    const mockArts = [{ name: 'gemma-4-e2b', url: '/artifacts/gemma.tar.gz', sha256: 'abc' }];
    mockFetch.mockReturnValue(mockJsonResponse(mockArts));

    const result = await api.artifacts();

    expect(mockFetch).toHaveBeenCalledWith('/api/artifacts');
    expect(result).toEqual(mockArts);
  });
});

describe('api.createDeployment', () => {
  it('posts deployment and returns result', async () => {
    const mockDep = { id: '1', model_id: 'gemma', status: 'pending' };
    mockFetch.mockReturnValue(mockJsonResponse(mockDep, 201));

    const payload = {
      model_id: 'gemma',
      artifact_url: 'http://cp:8080/artifacts/gemma.tar.gz',
      artifact_sha256: 'abc123',
      tag_selector: { location: 'barcelona' },
    };

    const result = await api.createDeployment(payload);

    expect(mockFetch).toHaveBeenCalledWith('/api/deployments', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    expect(result).toEqual(mockDep);
  });
});
