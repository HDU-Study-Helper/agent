#!/usr/bin/env python3
"""Validate Trigger contracts; --live batch-evaluates a temporary console agent.
Uses existing backend/scripts/agent_regression.py Client and env login credentials.
No API keys are read. --install adds/updates the current admin's Trigger instance.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
from jsonschema import Draft202012Validator, FormatChecker, ValidationError

ROOT = Path(__file__).resolve().parent
PROJECT = ROOT.parent.parent
SELECTION_PROTOCOL = 'indices_v1'

def prepare_case(fixture):
    """Convert historical fixtures in memory; never send their IDs to the model.

    Duplicate legacy IDs reuse their first index so invalid fixtures remain
    invalid. Missing IDs stay missing rather than silently repairing bad input.
    """
    case = copy.deepcopy(fixture)
    payload = case['input']
    payload.setdefault('selection_protocol', SELECTION_PROTOCOL)
    id_to_index = {}
    changes = payload.get('changes')
    if isinstance(changes, list):
        for position, change in enumerate(changes, 1):
            if not isinstance(change, dict):
                continue
            if 'change_id' in change:
                legacy_id = change.pop('change_id')
                if isinstance(legacy_id, str) and legacy_id:
                    change['index'] = id_to_index.setdefault(legacy_id, position)
            change.pop('evidence_ids', None)
    expected = case.get('expected')
    if expected is not None and 'relevant_change_ids' in expected:
        expected['relevant_indices'] = [id_to_index[value] for value in expected.pop('relevant_change_ids')]
    return case

def validate_input(payload, validator):
    validator.validate(payload)
    indices = [change['index'] for change in payload['changes']]
    if any(type(value) is not int for value in indices) or indices != list(range(1, len(indices) + 1)):
        raise ValueError('input indices must be consecutive 1-based page positions')

def validate_output(output, payload, validator):
    validator.validate(output)
    indices = output['relevant_indices']
    count = len(payload['changes'])
    if any(type(value) is not int or value < 1 or value > count for value in indices):
        raise ValueError('output index outside current page')
    if len(indices) != len(set(indices)):
        raise ValueError('duplicate output index')
    if indices != sorted(indices):
        raise ValueError('output indices must follow input order')
    if count and output['decision'] in ('TRIGGER', 'UNCERTAIN') and not indices:
        raise ValueError('nonempty relevant page requires selected indices')

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
    cases=[prepare_case(case) for case in json.loads((ROOT/'cases.json').read_text())]
    for case in cases:
        validate_input(case['input'],vin)
        validate_output(dict(case['expected'],reason='契约测试'),case['input'],vout)
    invalid=[prepare_case(case) for case in json.loads((ROOT/'invalid-inputs.json').read_text())]
    for case in invalid:
        try:
            validate_input(case['input'],vin)
        except (ValueError, ValidationError):
            continue
        raise AssertionError('invalid input accepted: '+case['name'])
    output_page={'changes':[{'index':1},{'index':2}]}
    for invalid_output in [
        '{"decision":"IGNORE","decision":"TRIGGER","reason":"x","relevant_indices":[]}',
        '```json\n{}\n```',
        '{"decision":"IGNORE","reason":"x","relevant_indices":[1]}',
        '{"decision":"UNKNOWN","reason":"x","relevant_indices":[]}',
        '{"decision":"IGNORE","reason":"x","relevant_indices":[],"workflow_id":"fake"}',
        '{"decision":"TRIGGER","reason":"x","relevant_change_ids":["c1"]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[1],"relevant_change_ids":["c1"]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[0]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[3]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[1,1]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[2,1]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[true]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[1.0]}',
        '{"decision":"TRIGGER","reason":"x","relevant_indices":[]}',
    ]:
        try:
            validate_output(strict_json(invalid_output),output_page,vout)
        except (ValueError, ValidationError):
            # Schema, page-selection and JSON parser failures are expected here.
            continue
        raise AssertionError('invalid output accepted')
    template=json.loads((ROOT/'definition.template.json').read_text())
    assert template['system_prompt']==(ROOT/'system-prompt.md').read_text(),'definition prompt drift'
    assert not template['enabled_tools'] and not template['permissions'],'Trigger must have no tools'
    report={'selection_protocol':SELECTION_PROTOCOL,'prompt_sha256':hashlib.sha256((ROOT/'system-prompt.md').read_bytes()).hexdigest(),'cases_sha256':hashlib.sha256((ROOT/'cases.json').read_bytes()).hexdigest(),'invalid_input_cases':len(invalid),'contract_cases':len(cases),'live_cases':[],'installed_agent_id':None}
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
        for key in ('selection_protocol','prompt_sha256','cases_sha256','model_config'):
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
                    validate_output(output,case['input'],vout)
                    item['decision']=output['decision'];item['relevant_indices']=output['relevant_indices']
                    item['run_id']=data['result']['run_id']
                    item['passed']=(output['decision']==case['expected']['decision'] and output['relevant_indices']==case['expected']['relevant_indices'] and data['record']['tool_calls']==0 and data['record']['model_calls']==1)
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
