import type { InstanceConfig } from '../../fork/types'
import { Box, Text } from 'ink'
import React, { useEffect } from 'react'

interface InstanceListProps {
  instances: Record<string, InstanceConfig>
  onDone: () => void
}

const COL = { name: 14, type: 8, scheme: 8, host: 28, token: 12 }
const TOTAL_WIDTH = COL.name + COL.type + COL.scheme + COL.host + COL.token

export function InstanceList({ instances, onDone }: InstanceListProps) {
  const entries = Object.entries(instances)

  useEffect(() => {
    const timer = setTimeout(onDone, 100)
    return () => clearTimeout(timer)
  }, [onDone])

  return (
    <Box flexDirection="column">
      <Box flexDirection="row">
        <Box minWidth={COL.name}><Text bold color="cyan">NAME</Text></Box>
        <Box minWidth={COL.type}><Text bold color="cyan">TYPE</Text></Box>
        <Box minWidth={COL.scheme}><Text bold color="cyan">SCHEME</Text></Box>
        <Box minWidth={COL.host}><Text bold color="cyan">HOST</Text></Box>
        <Box minWidth={COL.token}><Text bold color="cyan">TOKEN</Text></Box>
      </Box>
      <Box>
        <Text color="gray">{'─'.repeat(TOTAL_WIDTH)}</Text>
      </Box>
      {entries.map(([name, inst]) => {
        const tokenPreview = inst.token.length > 4 ? `${inst.token.slice(0, 4)}***` : '***'
        const scheme = inst.scheme ?? 'https'
        return (
          <Box key={name} flexDirection="row">
            <Box minWidth={COL.name}><Text bold>{name}</Text></Box>
            <Box minWidth={COL.type}>
              <Text color={inst.type === 'github' ? 'green' : 'magenta'}>{inst.type}</Text>
            </Box>
            <Box minWidth={COL.scheme}>
              <Text color={scheme === 'https' ? 'blue' : 'yellow'}>{scheme}</Text>
            </Box>
            <Box minWidth={COL.host}><Text>{inst.host}</Text></Box>
            <Box minWidth={COL.token}><Text color="gray">{tokenPreview}</Text></Box>
          </Box>
        )
      })}
      <Box marginTop={1}>
        <Text color="gray">
          {entries.length}
          {' '}
          instance
          {entries.length !== 1 ? 's' : ''}
          {' '}
          configured
        </Text>
      </Box>
    </Box>
  )
}
