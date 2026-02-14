import { cancel, log, outro } from '@clack/prompts'
import ansis from 'ansis'
import * as XLSX from 'xlsx'
import { printTitle } from './utils'

export interface Json2ExcelOptions {
  root: string
  keyPath?: string
}

/**
 * 通过点分隔的路径从对象中获取值
 * 例如: getValueByPath({a: {b: {c: 1}}}, 'a.b.c') => 1
 */
function getValueByPath(obj: any, path: string): any {
  const keys = path.split('.')
  let current = obj

  for (const key of keys) {
    if (current === null || current === undefined) {
      return undefined
    }
    current = current[key]
  }

  return current
}

export async function json2excel(options: Json2ExcelOptions): Promise<void> {
  printTitle()

  const { root, keyPath } = options

  // 读取JSON文件
  const file = Bun.file(root)
  const exists = await file.exists()

  if (!exists) {
    cancel(`File not found: ${root}`)
    return
  }

  let jsonData: any
  try {
    jsonData = await file.json()
  }
  catch (error) {
    cancel(`Failed to read or parse JSON file: ${error instanceof Error ? error.message : String(error)}`)
    return
  }

  // 如果提供了keyPath，通过路径提取数据
  let data = jsonData
  if (keyPath) {
    data = getValueByPath(jsonData, keyPath)
    if (data === undefined) {
      cancel(`Key path not found in JSON: ${keyPath}`)
      return
    }
  }

  // 确保数据是数组格式
  let arrayData: any[]
  if (Array.isArray(data)) {
    arrayData = data
  }
  else if (typeof data === 'object' && data !== null) {
    // 如果是对象，转换为单行数组
    arrayData = [data]
  }
  else {
    cancel('The extracted data is not an object or array, cannot convert to Excel.')
    return
  }

  if (arrayData.length === 0) {
    cancel('No data to convert to Excel.')
    return
  }

  // 创建工作表
  const worksheet = XLSX.utils.json_to_sheet(arrayData)

  // 创建工作簿
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, worksheet, 'Sheet1')

  // 生成输出文件名（与输入文件同名，扩展名改为.xlsx）
  const outputPath = root.replace(/\.(json|jsonc)$/i, '.xlsx')

  // 写入文件
  XLSX.writeFile(workbook, outputPath)

  log.success(`Converted JSON to Excel in ${ansis.cyan.underline(outputPath)}`)
  outro('Conversion completed successfully!')
}
