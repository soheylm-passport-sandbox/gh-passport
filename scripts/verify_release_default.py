#!/usr/bin/env python3
"""Refuse publication when the actual binary disagrees with qualified metadata."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--binary',type=Path,required=True)
p.add_argument('--tag',required=True)
p.add_argument('--source',type=Path,default=Path('SOURCE.json'))
a=p.parse_args()
s=json.loads(a.source.read_text(encoding='utf-8'))
assert s.get('source_exact') is True,'Unqualified source'
v=json.loads(subprocess.check_output([str(a.binary.resolve()),'version','--json']).decode('utf-8'))
assert v['version']==a.tag,(v['version'],a.tag)
assert v['curriculum_version']==s['curriculum_version'],(v['curriculum_version'],s['curriculum_version'])
assert str(v['controller_app_id'])==str(s['deployment']['controller_app_id'])
print('Actual release default, tag and controller match qualified SOURCE.json.')
