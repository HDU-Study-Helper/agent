#!/usr/bin/env python3
"""Install or explicitly update Filter using existing authenticated admin API.
Reads credentials only from environment; never outputs keys or passwords.
"""
import argparse,json,os,sys
import urllib.request,urllib.error,urllib.parse
from pathlib import Path
root=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(root/'backend/scripts'))
from agent_regression import Client

def list_owner_agents(call):
 agents=[];after='';seen=set()
 while True:
  page=call('/agents/instances'+('?after='+urllib.parse.quote(after,safe='') if after else ''))
  agents+=page['items'];after=page.get('next','')
  if not after:return agents
  if after in seen:raise RuntimeError('Agent pagination did not advance')
  seen.add(after)

def verify_installation(definition,created,readback,owner_id):
 if created.get('owner_user_id')!=owner_id or readback.get('owner_user_id')!=owner_id or readback.get('agent_id')!=created.get('agent_id'):
  raise RuntimeError('Installation ownership or identity mismatch')
 for key,value in definition.items():
  actual=created.get(key)
  matches=all(actual.get(k)==v for k,v in value.items()) if key=='limits' and isinstance(actual,dict) else actual==value
  if not matches or readback.get(key)!=actual:raise RuntimeError('Readback mismatch '+key)

def resolve_dispatch_targets(agents,owner_id):
 targets={}
 for role,placeholder in [('worker','{{WORKER_AGENT_ID}}'),('learner','{{LEARNER_AGENT_ID}}')]:
  matches=[v for v in agents if v.get('owner_user_id')==owner_id and v.get('agent_type')==role and v.get('status')=='enabled']
  if len(matches)!=1 or not matches[0].get('agent_id'):
   raise RuntimeError('Expected exactly one enabled '+role+' belonging to the authenticated account; configure targets before installing Filter')
  targets[placeholder]=matches[0]['agent_id']
 return targets

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--origin',default='http://127.0.0.1:2610')
p.add_argument('--update-agent-id',help='Explicit existing Filter ID to replace; otherwise refuse duplicate Filters')
a=p.parse_args()
c=Client(a.origin);actor=c.login(os.environ['BOOTSTRAP_ADMIN_USERNAME'],os.environ['BOOTSTRAP_ADMIN_PASSWORD'])
def call(path,method='GET',body=None,expected=200):
 if body is not None:
  headers={'Origin':a.origin,'Content-Type':'application/json'}
  for cookie in c.jar:
   if cookie.name=='academic_csrf':headers['X-CSRF-Token']=cookie.value
  req=urllib.request.Request(a.origin+'/api/v1'+path,method=method,headers=headers,data=json.dumps(body,ensure_ascii=False).encode())
  try:response=c.opener.open(req,timeout=60)
  except urllib.error.HTTPError as error:response=error
  with response:status=response.status;raw=response.read();data=json.loads(raw) if raw else None
 else:status,data,_=c.request('/api/v1'+path,method,body)
 if status!=expected:raise RuntimeError('API failed HTTP '+str(status)+' '+path)
 return data
agents=list_owner_agents(call)
existing=[v for v in agents if v['agent_type']=='filter']
if a.update_agent_id:
 if not any(v['agent_id']==a.update_agent_id for v in existing):raise RuntimeError('Specified existing Filter does not belong to current account')
elif existing:raise RuntimeError('Filter already exists; review it before explicitly selecting update-agent-id')
settings=call('/agents/settings');configured=[v for v in settings['providers'] if v.get('has_api_key') and v.get('default_model')]
if not configured:raise RuntimeError('No configured model with saved key and default model')
provider=configured[0]
definition=json.loads((Path(__file__).parent/'definition.template.json').read_text());definition['model_config']={'provider':provider['name'],'model':provider['default_model']}
for placeholder,target in resolve_dispatch_targets(agents,actor['id']).items():
 if placeholder not in definition['system_prompt']:raise RuntimeError('Filter template is missing target placeholder '+placeholder)
 definition['system_prompt']=definition['system_prompt'].replace(placeholder,target)
catalog={v['name']:v for v in call('/agents/tools')['items']}
assert set(definition['enabled_tools'])<=catalog.keys()
assert {permission for name in definition['enabled_tools'] for permission in catalog[name]['permissions']}==set(definition['permissions'])
endpoint='/agents/instances'+('/'+a.update_agent_id if a.update_agent_id else '')
created=call(endpoint,'PUT' if a.update_agent_id else 'POST',definition,200 if a.update_agent_id else 201)
readback=next(v for v in list_owner_agents(call) if v['agent_id']==created['agent_id'])
verify_installation(definition,created,readback,actor['id'])
report={'passed':True,'agent_id':created['agent_id'],'name':created['name'],'owner_user_id':created['owner_user_id'],'model_config':created['model_config'],'status':created['status'],'enabled_tools':created['enabled_tools'],'permissions':created['permissions'],'verified':['registered_tool_names','exact_permissions','owner_scope','persisted_prompt_and_configuration'],'model_run_performed':False}
(root/'.build').mkdir(parents=True,exist_ok=True)
(root/'.build/filter-installation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
