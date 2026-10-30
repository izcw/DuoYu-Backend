import fs from 'node:fs'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
process.chdir(fileURLToPath(new URL('..',import.meta.url)))
import { createRequire } from 'node:module'
const require=createRequire(import.meta.url), collection=require('@iconify-json/ri/icons.json'),names=new Set()
function walk(dir){for(const entry of fs.readdirSync(dir,{withFileTypes:true})){const file=path.join(dir,entry.name);if(entry.isDirectory())walk(file);else if(/\.(vue|ts)$/.test(file))for(const match of fs.readFileSync(file,'utf8').matchAll(/ri:([a-z0-9-]+)/g))names.add(match[1])}}
walk('src')
const icons={}
for(const name of names){if(!collection.icons[name])throw Error('Missing local icon: '+name);icons[name]=collection.icons[name]}
fs.writeFileSync('src/assets/local-icons.json',JSON.stringify({prefix:'ri',width:24,height:24,icons}))
console.log('Prepared '+names.size+' local icons')
