import type { CommitRecord } from '../types'
import { Box, Text } from 'ink'
import React, { useEffect } from 'react'

interface CommitTreeProps {
  commits: CommitRecord[]
  onDone: () => void
}

interface RepoGroup {
  repo: string
  commits: CommitRecord[]
}

export function CommitTree({ commits, onDone }: CommitTreeProps) {
  const groups = groupByRepo(commits)

  useEffect(() => {
    const timer = setTimeout(onDone, 100)
    return () => clearTimeout(timer)
  }, [onDone])

  return (
    <Box flexDirection="column">
      <Text bold color="green">
        Found
        {' '}
        {commits.length}
        {' '}
        commit
        {commits.length !== 1 ? 's' : ''}
        {' '}
        in
        {' '}
        {groups.length}
        {' '}
        repositor
        {groups.length !== 1 ? 'ies' : 'y'}
      </Text>
      <Text></Text>
      {groups.map((group, gi) => (
        <React.Fragment key={group.repo}>
          <Box flexDirection="column" marginBottom={gi < groups.length - 1 ? 1 : 0}>
            <Text>
              <Text bold color="magenta">
                📦
                {' '}
                {group.repo}
              </Text>
              <Text color="gray">
                {' '}
                (
                {group.commits.length}
                {' '}
                commit
                {group.commits.length !== 1 ? 's' : ''}
                )
              </Text>
            </Text>
            {group.commits.map((commit, ci) => {
              const isLast = ci === group.commits.length - 1
              const connector = isLast ? '└── ' : '├── '
              return (
                <React.Fragment key={`${group.repo}-${commit.date}-${ci}`}>
                  <Text>
                    <Text color="gray">{connector}</Text>
                    <Text color="blue">{commit.date}</Text>
                    <Text>  </Text>
                    <Text color="yellow">{commit.author}</Text>
                    <Text>  </Text>
                    <Text>{commit.message}</Text>
                  </Text>
                </React.Fragment>
              )
            })}
          </Box>
        </React.Fragment>
      ))}
    </Box>
  )
}

function groupByRepo(commits: CommitRecord[]): RepoGroup[] {
  const map = new Map<string, CommitRecord[]>()

  for (const c of commits) {
    let list = map.get(c.repo)
    if (!list) {
      list = []
      map.set(c.repo, list)
    }
    list.push(c)
  }

  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([repo, commits]) => ({
      repo,
      commits: commits.sort((a, b) => b.date.localeCompare(a.date)),
    }))
}
