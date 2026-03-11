import { describe, it, expect } from 'vitest'
import { truncDigest, formatTime, logStatusMap } from './updates'

describe('truncDigest', () => {
  it('returns em-dash for empty string', () => {
    expect(truncDigest('')).toBe('—')
  })

  it('returns em-dash for undefined-like falsy', () => {
    expect(truncDigest(undefined as unknown as string)).toBe('—')
  })

  it('truncates sha256 digest to 19 chars + ellipsis', () => {
    const digest = 'sha256:abc123def456789012345678901234567890123456789012345678901234abcd'
    expect(truncDigest(digest)).toBe('sha256:abc123def456…')
    expect(truncDigest(digest).length).toBe(20) // 19 chars + ellipsis
  })

  it('returns short sha256 digest truncated', () => {
    expect(truncDigest('sha256:abcdef')).toBe('sha256:abcdef…')
  })

  it('returns non-digest version as-is', () => {
    expect(truncDigest('v3.2.1')).toBe('v3.2.1')
  })

  it('returns tag version as-is', () => {
    expect(truncDigest('30.2')).toBe('30.2')
  })

  it('returns commit hash as-is', () => {
    expect(truncDigest('a1b2c3d4e5f6')).toBe('a1b2c3d4e5f6')
  })
})

describe('formatTime', () => {
  it('returns empty string for empty input', () => {
    expect(formatTime('')).toBe('')
  })

  it('returns original string for invalid date', () => {
    expect(formatTime('not-a-date')).toBe('not-a-date')
  })

  it('formats valid ISO date', () => {
    const result = formatTime('2026-03-10T14:30:00Z')
    // Should contain date components (locale-dependent but de-DE uses dd.mm.yy)
    expect(result).toBeTruthy()
    expect(result.length).toBeGreaterThan(0)
  })

  it('formats ISO date with timezone', () => {
    const result = formatTime('2026-03-10T14:30:00+01:00')
    expect(result).toBeTruthy()
  })
})

describe('logStatusMap', () => {
  it('maps done to running', () => {
    expect(logStatusMap('done')).toBe('running')
  })

  it('maps failed to critical', () => {
    expect(logStatusMap('failed')).toBe('critical')
  })

  it('maps rolled_back to warning', () => {
    expect(logStatusMap('rolled_back')).toBe('warning')
  })

  it('maps pulling to degraded', () => {
    expect(logStatusMap('pulling')).toBe('degraded')
  })

  it('maps building to degraded', () => {
    expect(logStatusMap('building')).toBe('degraded')
  })

  it('maps restarting to degraded', () => {
    expect(logStatusMap('restarting')).toBe('degraded')
  })

  it('maps pending to unknown', () => {
    expect(logStatusMap('pending')).toBe('unknown')
  })

  it('maps empty string to unknown', () => {
    expect(logStatusMap('')).toBe('unknown')
  })

  it('maps unrecognized status to unknown', () => {
    expect(logStatusMap('something_else')).toBe('unknown')
  })
})
