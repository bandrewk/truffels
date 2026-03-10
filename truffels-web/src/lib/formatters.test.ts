import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  formatUptime,
  formatBytes,
  formatDifficulty,
  formatLargeNumber,
  formatHashrate,
} from './formatters'

describe('formatUptime', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-10T12:00:00Z'))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns - for empty input', () => {
    expect(formatUptime('')).toBe('-')
  })

  it('returns - for invalid date', () => {
    expect(formatUptime('not-a-date')).toBe('-')
  })

  it('formats seconds', () => {
    const thirtySecsAgo = new Date('2026-03-10T11:59:30Z').toISOString()
    expect(formatUptime(thirtySecsAgo)).toBe('30s')
  })

  it('formats minutes', () => {
    const fiveMinsAgo = new Date('2026-03-10T11:55:00Z').toISOString()
    expect(formatUptime(fiveMinsAgo)).toBe('5m')
  })

  it('formats hours and minutes', () => {
    const twoHoursAgo = new Date('2026-03-10T09:45:00Z').toISOString()
    expect(formatUptime(twoHoursAgo)).toBe('2h 15m')
  })

  it('formats days and hours', () => {
    const threeDaysAgo = new Date('2026-03-07T06:00:00Z').toISOString()
    expect(formatUptime(threeDaysAgo)).toBe('3d 6h')
  })
})

describe('formatBytes', () => {
  it('formats bytes', () => {
    expect(formatBytes(500)).toBe('500 B')
  })

  it('formats kilobytes', () => {
    expect(formatBytes(2048)).toBe('2.0 KB')
  })

  it('formats megabytes', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
  })

  it('formats gigabytes', () => {
    expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe('2.5 GB')
  })

  it('formats zero', () => {
    expect(formatBytes(0)).toBe('0 B')
  })
})

describe('formatDifficulty', () => {
  it('formats trillions', () => {
    expect(formatDifficulty(92.05e12)).toBe('92.05T')
  })

  it('formats billions', () => {
    expect(formatDifficulty(38.57e9)).toBe('38.57G')
  })

  it('formats millions', () => {
    expect(formatDifficulty(1.5e6)).toBe('1.50M')
  })

  it('formats small numbers', () => {
    expect(formatDifficulty(12345)).toBe('12345')
  })
})

describe('formatLargeNumber', () => {
  it('formats trillions with space', () => {
    expect(formatLargeNumber(1.5e12)).toBe('1.50 T')
  })

  it('formats billions with space', () => {
    expect(formatLargeNumber(38.57e9)).toBe('38.57 G')
  })

  it('formats millions with space', () => {
    expect(formatLargeNumber(2.5e6)).toBe('2.50 M')
  })

  it('formats thousands with space', () => {
    expect(formatLargeNumber(1500)).toBe('1.50 K')
  })

  it('formats small numbers as-is', () => {
    expect(formatLargeNumber(42)).toBe('42')
  })

  it('formats zero', () => {
    expect(formatLargeNumber(0)).toBe('0')
  })
})

describe('formatHashrate', () => {
  it('formats zero', () => {
    expect(formatHashrate('0')).toBe('0 H/s')
  })

  it('formats empty string', () => {
    expect(formatHashrate('')).toBe('0 H/s')
  })

  it('formats megahash', () => {
    expect(formatHashrate('1.92M')).toBe('1.92 MH/s')
  })

  it('formats gigahash', () => {
    expect(formatHashrate('655G')).toBe('655 GH/s')
  })

  it('formats terahash', () => {
    expect(formatHashrate('1.5T')).toBe('1.5 TH/s')
  })

  it('formats kilohash', () => {
    expect(formatHashrate('500K')).toBe('500 KH/s')
  })

  it('formats bare number', () => {
    expect(formatHashrate('12345')).toBe('12345 H/s')
  })

  it('passes through unrecognized format', () => {
    expect(formatHashrate('unknown format')).toBe('unknown format')
  })
})
