import { describe, it, expect } from 'vitest'
import { classifyLine, severityAtOrAbove, SEVERITY_LEVELS } from './logUtils'

describe('classifyLine', () => {
  it('classifies JSON error logs', () => {
    expect(classifyLine('{"level":"error","msg":"fail"}')).toBe('error')
    expect(classifyLine('{"level":"fatal","msg":"crash"}')).toBe('error')
    expect(classifyLine('{"lvl":"panic","msg":"oom"}')).toBe('error')
  })

  it('classifies JSON warn logs', () => {
    expect(classifyLine('{"level":"warn","msg":"slow"}')).toBe('warn')
    expect(classifyLine('{"level":"warning","msg":"high mem"}')).toBe('warn')
  })

  it('classifies JSON info logs', () => {
    expect(classifyLine('{"level":"info","msg":"started"}')).toBe('info')
    expect(classifyLine('{"level":"notice","msg":"ok"}')).toBe('info')
  })

  it('classifies JSON debug logs', () => {
    expect(classifyLine('{"level":"debug","msg":"trace"}')).toBe('debug')
    expect(classifyLine('{"level":"trace","msg":"verbose"}')).toBe('debug')
  })

  it('classifies plain text errors', () => {
    expect(classifyLine('Mar 13 ERROR failed to start')).toBe('error')
    expect(classifyLine('FATAL: cannot bind port')).toBe('error')
    expect(classifyLine('CRITICAL memory low')).toBe('error')
  })

  it('classifies plain text warnings', () => {
    expect(classifyLine('Mar 13 WARNING disk usage high')).toBe('warn')
    expect(classifyLine('WARN: slow query')).toBe('warn')
  })

  it('classifies plain text info', () => {
    expect(classifyLine('Mar 13 INFO service started')).toBe('info')
    expect(classifyLine('NOTICE: config reloaded')).toBe('info')
  })

  it('classifies plain text debug', () => {
    expect(classifyLine('DEBUG processing block 123')).toBe('debug')
    expect(classifyLine('TRACE: entering function')).toBe('debug')
  })

  it('returns unknown for unclassifiable lines', () => {
    expect(classifyLine('plain text without severity')).toBe('unknown')
    expect(classifyLine('192.168.0.1 - - [13/Mar/2026] "GET /"')).toBe('unknown')
  })

  it('handles invalid JSON gracefully', () => {
    expect(classifyLine('{not valid json}')).toBe('unknown')
  })
})

describe('severityAtOrAbove', () => {
  it('returns only error for error threshold', () => {
    const set = severityAtOrAbove('error')
    expect(set.has('error')).toBe(true)
    expect(set.has('warn')).toBe(false)
    expect(set.has('info')).toBe(false)
  })

  it('returns error+warn for warn threshold', () => {
    const set = severityAtOrAbove('warn')
    expect(set.has('error')).toBe(true)
    expect(set.has('warn')).toBe(true)
    expect(set.has('info')).toBe(false)
  })

  it('returns error+warn+info for info threshold', () => {
    const set = severityAtOrAbove('info')
    expect(set.has('error')).toBe(true)
    expect(set.has('warn')).toBe(true)
    expect(set.has('info')).toBe(true)
    expect(set.has('debug')).toBe(false)
  })

  it('returns all levels for debug threshold', () => {
    const set = severityAtOrAbove('debug')
    expect(set.size).toBe(4)
  })
})

describe('SEVERITY_LEVELS', () => {
  it('is ordered by importance', () => {
    expect(SEVERITY_LEVELS).toEqual(['error', 'warn', 'info', 'debug'])
  })
})
