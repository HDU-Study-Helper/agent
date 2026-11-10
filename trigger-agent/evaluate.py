#!/usr/bin/env python3
"""Validate Trigger contracts; --live batch-evaluates a temporary console agent.
Uses existing backend/scripts/agent_regression.py Client and env login credentials.
No API keys are read. --install adds/updates the current admin's Trigger instance.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
from jsonschema import Draft202012Validator, FormatChecker, ValidationError

ROOT = Path(__file__).resolve().parent
PROJECT = ROOT.parent.parent

def strict_json(raw):
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result:raise ValueError('duplicate JSON key')
            result[key]=value
        return result
    return json.loads(raw,object_pairs_hook=unique,parse_constant=lambda value:(_ for _ in ()).throw(ValueError('non-JSON number')))

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--live',action='store_true')
    parser.add_argument('--install',action='store_true')
    parser.add_argument('--retry-failed',type=Path,help='Retry failed cases from a report for this exact prompt/case set; preserve prior attempts')
    parser.add_argument('--base-url',default='http://127.0.0.1:2610')
    parser.add_argument('--output',default=str(PROJECT/'.build/trigger-agent-evaluation.json'))
    args=parser.parse_args()
    if args.retry_failed and not args.live:parser.error('--retry-failed requires --live')
    input_schema=json.loads((ROOT/'input.schema.json').read_text())
    output_schema=json.loads((ROOT/'output.schema.json').read_text())
    for schema in (input_schema,output_schema):Draft202012Validator.check_schema(schema)
    vin=Draft202012Validator(input_schema,format_checker=FormatChecker())
    vout=Draft202012Validator(output_schema)
    cases=json.loads((ROOT/'cases.json').read_text())
    for case in cases:
        vin.validate(case['input'])
        ids=[c['change_id'] for c in case['input']['changes']]
        assert len(ids)==len(set(ids)),case['name']
        vout.validate(dict(case['expected'],reason='契约测试'))
        assert set(case['expected']['relevant_change_ids'])<=set(ids),case['name']
    invalid=json.loads((ROOT/'invalid-inputs.json').read_text())
    for case in invalid:
        errors=list(vin.iter_errors(case['input']))
        ids=[c.get('change_id') for c in case['input'].get('changes',[]) or [] if isinstance(c,dict)]
        assert errors or len(ids)!=len(set(ids)),case['name']
    for invalid_output in [
        '{"decision":"IGNORE","decision":"TRIGGER","reason":"x","relevant_change_ids":[]}',
        '```json\n{}\n```',
        '{"decision":"IGNORE","reason":"x","relevant_change_ids":["c1"]}',
        '{"decision":"UNKNOWN","reason":"x","relevant_change_ids":[]}',
        '{"decision":"IGNORE","reason":"x","relevant_change_ids":[],"workflow_id":"fake"}',
    ]:
        try:
            vout.validate(strict_json(invalid_output))
        except (ValueError, ValidationError):
            # Schema and JSON parser failures are both expected here.
            continue
        raise AssertionError('invalid output accepted')
    template=json.loads((ROOT/'definition.template.json').read_text())
    assert template['system_prompt']==(ROOT/'system-prompt.md').read_text(),'definition prompt drift'
    assert not template['enabled_tools'] and not template['permissions'],'Trigger must have no tools'
    report={'prompt_sha256':hashlib.sha256((ROOT/'system-prompt.md').read_bytes()).hexdigest(),'cases_sha256':hashlib.sha256((ROOT/'cases.json').read_bytes()).hexdigest(),'invalid_input_cases':len(invalid),'contract_cases':len(cases),'live_cases':[],'installed_agent_id':None}
    print(json.dumps({'contracts':'passed','cases':len(cases)},ensure_ascii=False),flush=True)
    if not (args.live or args.install):return
    spec=importlib.util.spec_from_file_location('agent_regression',PROJECT/'backend/scripts/agent_regression.py')
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    client=module.Client(args.base_url)
    client.login(os.environ.get('AGENT_TEST_USERNAME') or os.environ['BOOTSTRAP_ADMIN_USERNAME'],os.environ.get('AGENT_TEST_PASSWORD') or os.environ['BOOTSTRAP_ADMIN_PASSWORD'])
    def request(path,method='GET',body=None,expected=200):
        status,data,_=client.request('/api/v1'+path,method,body,timeout=90)
        if status!=expected:raise RuntimeError('HTTP %d on %s: %s'%(status,path,(data or {}).get('error',{}).get('code','unexpected response')))
        return data
    settings=request('/agents/settings')
    provider=next((p for p in settings['providers'] if p['has_api_key']),None)
    if not provider:raise RuntimeError('No configured model provider')
    definition=json.loads((ROOT/'definition.template.json').read_text())
    definition['model_config']={'provider':provider['name'],'model':provider['default_model']}
    report['model_config']=definition['model_config']
    previous={}
    if args.retry_failed:
        prior=json.loads(args.retry_failed.read_text())
        for key in ('prompt_sha256','cases_sha256','model_config'):
            if prior.get(key)!=report[key]:raise RuntimeError('Cannot reuse report with different '+key)
        previous={c['name']:c for c in prior['live_cases']}
        if set(previous)!={c['name'] for c in cases}:raise RuntimeError('Incomplete prior case set')
    user=None
    try:
        if args.live:
            user=request('/users','POST',{'username':'trigger_eval_'+secrets.token_hex(5),'display_name':'Trigger temporary evaluation','password':secrets.token_urlsafe(24),'role':'user'},201)
            query='?owner_id='+user['id']
            agent=request('/agents/instances'+query,'POST',definition,201)
            for case in cases:
                prior=previous.get(case['name'])
                if prior and prior['passed']:
                    report['live_cases'].append(prior)
                    continue
                item={'name':case['name'],'passed':False}
                try:
                    status,data,seconds=client.request('/api/v1/agents/instances/'+agent['agent_id']+'/runs'+query,'POST',{'input':case['input']},timeout=90)
                    item.update(status=status,seconds=seconds)
                    if status!=200:
                        item['error']=(data or {}).get('error');raise ValueError('run failed')
                    # Strict JSON: no markdown or leading/trailing non-JSON text.
                    output=strict_json(data['result']['output'])
                    vout.validate(output)
                    valid_ids=[c['change_id'] for c in case['input']['changes']]
                    assert set(output['relevant_change_ids'])<=set(valid_ids),'invented ID'
                    item['decision']=output['decision'];item['relevant_change_ids']=output['relevant_change_ids']
                    item['run_id']=data['result']['run_id']
                    item['passed']=(output['decision']==case['expected']['decision'] and output['relevant_change_ids']==case['expected']['relevant_change_ids'] and data['record']['tool_calls']==0 and data['record']['model_calls']==1)
                except Exception as error:item.setdefault('error',type(error).__name__)
                if prior:item['prior_attempts']=prior.get('prior_attempts',[])+[{k:v for k,v in prior.items() if k!='prior_attempts'}]
                report['live_cases'].append(item);print(json.dumps(item,ensure_ascii=False),flush=True)
        if args.install:
            if any(not c['passed'] for c in report['live_cases']):raise RuntimeError('Evaluation failed; installation withheld')
            agents=request('/agents/instances')['items']
            existing=next((a for a in agents if a['agent_type']=='trigger' and a['name']==definition['name']),None)
            if existing:
                # Stateful instances must not silently be repurposed for this stateless classifier.
                memories=request('/agents/instances/'+existing['agent_id']+'/memories')['items']
                if memories:raise RuntimeError('Existing Trigger has memories; refusing to repurpose')
            agent=request('/agents/instances'+('/'+existing['agent_id'] if existing else ''),'PUT' if existing else 'POST',definition,200 if existing else 201)
            report['installed_agent_id']=agent['agent_id']
            print(json.dumps({'installed_agent_id':agent['agent_id']},ensure_ascii=False))
    finally:
        if user:
            request('/users/'+user['id'],'DELETE',expected=204)
        report['summary']={'passed':sum(c['passed'] for c in report['live_cases']),'failed':sum(not c['passed'] for c in report['live_cases'])}
        path=Path(args.output);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    if report['summary']['failed']:raise SystemExit(1)

if __name__=='__main__':main()
