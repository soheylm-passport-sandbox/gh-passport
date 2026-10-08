"""Versioned bounded Python assessment. Parse learner text; never execute it."""
import ast
import io
import os
import stat
import tokenize
from pathlib import Path

CONTRACT = "python-change/2"
SOURCE = "workspace/python_project/passport_example.py"
TESTS = "workspace/python_project/tests/test_passport_example.py"
PATHS = frozenset((SOURCE, TESTS))
LIMIT = 100_000
MESSAGES = {
    "scope": "Change only the source and test files in this exercise; keep both changes.",
    "files": "Provide both declared Python files as regular UTF-8 files, within 100 KB each.",
    "syntax": "Repair Python syntax or encoding before checking the calculation.",
    "shape": "Use the bounded teaching format: one function, integer arithmetic with +, -, * and discoverable unittest methods. No extra imports, calls, loops or decorators.",
    "complexity": "Simplify this small exercise; its expression or file exceeds the analysis limits.",
    "validation": "Keep ValueError for either non-positive input, while allowing every positive integer pair.",
    "behavior": "The calculation must equal CPU count multiplied by memory per CPU for positive integers.",
    "discovery": "Keep at least two distinct methods starting with test (for example test_three_cpus) inside unittest.TestCase, without skip decorators or conditional assertions.",
    "expected": "Calculate expected values independently; do not obtain them by calling the function being tested.",
    "baseline": "Keep the original one-CPU assertion: input (1, 3), expected 3.",
    "regression": "Add a discovered test with more than one CPU and positive memory; independently calculate its expected total.",
}


class Rejected(Exception):
    def __init__(self, code, status="needs_work"):
        self.code, self.status = code, status


def reject(code, status="needs_work"):
    raise Rejected(code, status)


def tree(raw):
    if not isinstance(raw, bytes) or len(raw) > LIMIT:
        reject("files", "blocked")
    try:
        text = raw.decode("utf-8-sig")
        # Bound numeric tokens before the AST parser converts them to integers.
        for token in tokenize.generate_tokens(io.StringIO(text).readline):
            if token.type == tokenize.NUMBER and len(token.string) > 32:
                reject("complexity", "unsupported")
        result = ast.parse(text)
    except (UnicodeError, SyntaxError, ValueError, tokenize.TokenError, IndentationError):
        reject("syntax")
    except (RecursionError, MemoryError):
        reject("complexity", "unsupported")
    if sum(1 for _ in ast.walk(result)) > 512:
        reject("complexity", "unsupported")
    return result


def without_doc(nodes):
    if nodes and isinstance(nodes[0], ast.Expr) and isinstance(nodes[0].value, ast.Constant) and isinstance(nodes[0].value.value, str):
        return nodes[1:]
    return nodes


def integer(value):
    return type(value) is int and abs(value) <= 1_000_000


def poly(node, names, depth=0):
    """Normalize bounded arithmetic exactly, rather than sample a few outputs."""
    if depth > 32:
        reject("complexity", "unsupported")
    if isinstance(node, ast.Name) and node.id in names:
        return names[node.id].copy()
    if isinstance(node, ast.Constant) and integer(node.value):
        return {(0, 0): node.value} if node.value else {}
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
        sign = -1 if isinstance(node.op, ast.USub) else 1
        return {k: sign * v for k, v in poly(node.operand, names, depth + 1).items()}
    if not isinstance(node, ast.BinOp) or not isinstance(node.op, (ast.Add, ast.Sub, ast.Mult)):
        reject("shape", "unsupported")
    left, right = poly(node.left, names, depth + 1), poly(node.right, names, depth + 1)
    result = {}
    if isinstance(node.op, ast.Mult):
        for (a, b), x in left.items():
            for (c, d), y in right.items():
                key = (a + c, b + d)
                if sum(key) > 4:
                    reject("complexity", "unsupported")
                result[key] = result.get(key, 0) + x * y
    else:
        result = left.copy()
        for key, value in right.items():
            result[key] = result.get(key, 0) + (-value if isinstance(node.op, ast.Sub) else value)
    result = {key: value for key, value in result.items() if value}
    if len(result) > 32 or any(abs(v) > 10**12 for v in result.values()):
        reject("complexity", "unsupported")
    return result


def constant(node, names):
    if any(isinstance(n, ast.Call) for n in ast.walk(node)):
        reject("expected")
    result = poly(node, names)
    if any(key != (0, 0) for key in result):
        reject("expected")
    value = result.get((0, 0), 0)
    if not integer(value):
        reject("complexity", "unsupported")
    return value


def condition(node, values, depth=0):
    if depth > 32:
        reject("complexity", "unsupported")
    if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
        results = [condition(n, values, depth + 1) for n in node.values]
        return all(results) if isinstance(node.op, ast.And) else any(results)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return not condition(node.operand, values, depth + 1)
    if (isinstance(node, ast.Compare) and isinstance(node.left, ast.Name)
            and node.left.id in values and len(node.ops) == len(node.comparators) == 1
            and isinstance(node.comparators[0], ast.Constant)):
        pair = (type(node.ops[0]), node.comparators[0].value)
        if type(pair[1]) is not int:
            reject("shape", "unsupported")
        predicates = {(ast.Lt, 1): lambda v: v < 1, (ast.LtE, 0): lambda v: v <= 0,
                      (ast.GtE, 1): lambda v: v >= 1, (ast.Gt, 0): lambda v: v > 0}
        if pair in predicates:
            return predicates[pair](values[node.left.id])
    reject("shape", "unsupported")


def signature(fn, names, source=False):
    args = fn.args
    if (not isinstance(fn, ast.FunctionDef) or fn.decorator_list or getattr(fn, "type_params", [])
            or args.posonlyargs or args.kwonlyargs or args.defaults or args.kw_defaults
            or args.vararg or args.kwarg or [a.arg for a in args.args] != names):
        reject("shape", "unsupported")
    for arg in args.args:
        if arg.annotation is not None and not (source and isinstance(arg.annotation, ast.Name) and arg.annotation.id == "int"):
            reject("shape", "unsupported")
    if fn.returns is not None:
        valid = isinstance(fn.returns, ast.Name) and fn.returns.id == "int" if source else isinstance(fn.returns, ast.Constant) and fn.returns.value is None
        if not valid:
            reject("shape", "unsupported")


def source_model(module):
    body = without_doc(module.body)
    if body and isinstance(body[0], ast.ImportFrom) and body[0].module == "__future__" and body[0].level == 0 and [(a.name, a.asname) for a in body[0].names] == [("annotations", None)]:
        body = body[1:]
    if len(body) != 1 or not isinstance(body[0], ast.FunctionDef) or body[0].name != "total_memory_gib":
        reject("shape", "unsupported")
    fn = body[0]
    signature(fn, ["cpus", "memory_per_cpu_gib"], source=True)
    statements = without_doc(fn.body)
    guards = []
    while statements and isinstance(statements[0], ast.If):
        guard, statements = statements[0], statements[1:]
        if guard.orelse or len(guard.body) != 1 or not isinstance(guard.body[0], ast.Raise):
            reject("shape", "unsupported")
        raised = guard.body[0]
        call = raised.exc
        if (raised.cause is not None or not isinstance(call, ast.Call) or not isinstance(call.func, ast.Name)
                or call.func.id != "ValueError" or call.keywords or len(call.args) > 1
                or (call.args and not (isinstance(call.args[0], ast.Constant) and isinstance(call.args[0].value, str) and len(call.args[0].value) <= 256))):
            reject("shape", "unsupported")
        guards.append(guard.test)
    # Allowed predicates distinguish only positive/non-positive integer inputs.
    # These representatives therefore prove the validation for the whole domain.
    for cpu in [-1, 0, 1]:
        for memory in [-1, 0, 1]:
            results = [condition(g, {"cpus": cpu, "memory_per_cpu_gib": memory}) for g in guards]
            if any(results) != (cpu <= 0 or memory <= 0):
                reject("validation")
    names = {"cpus": {(1, 0): 1}, "memory_per_cpu_gib": {(0, 1): 1}}
    if not statements or not isinstance(statements[-1], ast.Return):
        reject("shape", "unsupported")
    for statement in statements[:-1]:
        if not isinstance(statement, ast.Assign) or len(statement.targets) != 1 or not isinstance(statement.targets[0], ast.Name):
            reject("shape", "unsupported")
        name = statement.targets[0].id
        if name in ("cpus", "memory_per_cpu_gib", "ValueError"):
            reject("shape", "unsupported")
        names[name] = poly(statement.value, names)
    result = poly(statements[-1].value, names)
    if result != {(1, 1): 1}:
        reject("behavior")


def function_input(call, names):
    if not isinstance(call, ast.Call) or not isinstance(call.func, ast.Name) or call.func.id != "total_memory_gib":
        reject("expected")
    values = dict(zip(["cpus", "memory_per_cpu_gib"], call.args))
    if len(call.args) > 2:
        reject("shape", "unsupported")
    for item in call.keywords:
        if item.arg not in ("cpus", "memory_per_cpu_gib") or item.arg in values:
            reject("shape", "unsupported")
        values[item.arg] = item.value
    if set(values) != {"cpus", "memory_per_cpu_gib"}:
        reject("shape", "unsupported")
    pair = tuple(constant(values[key], names) for key in ["cpus", "memory_per_cpu_gib"])
    if any(abs(v) > 1000 for v in pair):
        reject("complexity", "unsupported")
    return pair


def test_cases(module):
    body = without_doc(module.body)
    classes, imports = [], []
    main = ast.dump(ast.parse('if __name__ == "__main__":\n    unittest.main()').body[0], include_attributes=False)
    for node in body:
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            imports.append(ast.dump(node, include_attributes=False))
        elif isinstance(node, ast.ClassDef):
            classes.append(node)
        elif ast.dump(node, include_attributes=False) != main:
            reject("shape", "unsupported")
    expected_imports = [ast.dump(ast.parse(s).body[0], include_attributes=False) for s in ["import unittest", "from passport_example import total_memory_gib"]]
    if sorted(imports) != sorted(expected_imports) or len(classes) != 1:
        reject("shape", "unsupported")
    cls = classes[0]
    if (cls.name.startswith("__") or cls.name in ("unittest", "total_memory_gib", "load_tests", "setUpModule", "tearDownModule", "ValueError") or cls.decorator_list or cls.keywords or getattr(cls, "type_params", []) or len(cls.bases) != 1
            or ast.dump(cls.bases[0]) != ast.dump(ast.parse('unittest.TestCase', mode='eval').body)):
        reject("discovery")
    methods = without_doc(cls.body)
    if len(methods) < 2 or len({getattr(m, "name", None) for m in methods}) != len(methods):
        reject("discovery")
    cases = []
    for method in methods:
        if not isinstance(method, ast.FunctionDef) or not method.name.startswith("test") or method.decorator_list:
            reject("discovery")
        signature(method, ["self"])
        names, assertions = {}, 0
        for statement in without_doc(method.body):
            if isinstance(statement, ast.Assign) and len(statement.targets) == 1 and isinstance(statement.targets[0], ast.Name) and statement.targets[0].id not in ("self", "unittest", "total_memory_gib", "ValueError"):
                name = statement.targets[0].id
                names[name] = {(0, 0): constant(statement.value, names)}
                continue
            if isinstance(statement, ast.With):
                if len(statement.items) != 1 or statement.items[0].optional_vars is not None or len(statement.body) != 1:
                    reject("shape", "unsupported")
                manager = statement.items[0].context_expr
                if (not isinstance(manager, ast.Call) or not isinstance(manager.func, ast.Attribute)
                        or not isinstance(manager.func.value, ast.Name) or manager.func.value.id != "self"
                        or manager.func.attr != "assertRaises" or manager.keywords or len(manager.args) != 1
                        or not isinstance(manager.args[0], ast.Name) or manager.args[0].id != "ValueError"
                        or not isinstance(statement.body[0], ast.Expr)):
                    reject("shape", "unsupported")
                cpu, memory = function_input(statement.body[0].value, names)
                if cpu > 0 and memory > 0:
                    reject("expected")
                assertions += 1
                continue
            if not isinstance(statement, ast.Expr) or not isinstance(statement.value, ast.Call):
                reject("discovery")
            call = statement.value
            if (not isinstance(call.func, ast.Attribute) or not isinstance(call.func.value, ast.Name)
                    or call.func.value.id != "self" or call.func.attr != "assertEqual" or len(call.args) != 2 or call.keywords):
                reject("shape", "unsupported")
            left, right = call.args
            if not isinstance(left, ast.Call) and isinstance(right, ast.Call):
                left, right = right, left
            cpu, memory = function_input(left, names)
            expected = constant(right, names)
            if cpu < 1 or memory < 1 or expected != cpu * memory:
                reject("expected")
            cases.append((method.name, cpu, memory, expected))
            assertions += 1
        if not assertions:
            reject("discovery")
    if not any((c, m, e) == (1, 3, 3) for _, c, m, e in cases):
        reject("baseline")
    regressions = [case for case in cases if case[1] > 1]
    if not regressions or not any(a[0] != b[0] for a in cases for b in regressions if a[1:] == (1, 3, 3)):
        reject("regression")
    return cases, regressions[0][1:]


def assess_remote(files, changed_paths, file_modes):
    """Candidate boundary for trusted Git blob/diff inputs, not learner receipts."""
    try:
        if not isinstance(files, dict) or len(files) != 2 or set(files) != PATHS:
            reject("files", "blocked")
        if not isinstance(file_modes, dict) or len(file_modes) != 2 or set(file_modes) != PATHS or any(mode not in ("100644", "100755") for mode in file_modes.values()):
            reject("files", "blocked")
        if (not isinstance(changed_paths, (list, tuple)) or len(changed_paths) != 2
                or any(not isinstance(p, str) for p in changed_paths) or set(changed_paths) != PATHS):
            reject("scope")
        source, tests = tree(files[SOURCE]), tree(files[TESTS])
        source_model(source)
        cases, regression = test_cases(tests)
        return {"contract": CONTRACT, "status": "pass", "code": "accepted", "test_methods": len({case[0] for case in cases}),
                "regression": list(regression), "baseline_fails_regression": regression[1] != regression[2]}
    except (RecursionError, MemoryError):
        return {"contract": CONTRACT, "status": "unsupported", "code": "complexity", "message": MESSAGES["complexity"]}
    except Rejected as error:
        return {"contract": CONTRACT, "status": error.status, "code": error.code, "message": MESSAGES[error.code]}


def assess_local(root, changed_paths):
    """Read only the two fixed regular files; never run unittest or learner code."""
    root = Path(root)
    files, modes = {}, {}
    try:
        if root.is_symlink():
            reject("files", "blocked")
        resolved_root = root.resolve(strict=True)
        for relative in PATHS:
            path = root
            for part in relative.split("/"):
                path = path / part
                if path.is_symlink():
                    reject("files", "blocked")
            try:
                path.resolve(strict=True).relative_to(resolved_root)
            except ValueError:
                reject("files", "blocked")
            before = path.lstat()
            if not stat.S_ISREG(before.st_mode):
                reject("files", "blocked")
            fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
            with os.fdopen(fd, "rb") as stream:
                opened = os.fstat(stream.fileno())
                if not stat.S_ISREG(opened.st_mode) or (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
                    reject("files", "blocked")
                files[relative] = stream.read(LIMIT + 1)
            after = path.lstat()
            if (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns) != (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) or path.is_symlink():
                reject("files", "blocked")
            modes[relative] = "100755" if before.st_mode & 0o111 else "100644"
        return assess_remote(files, changed_paths, modes)
    except (OSError, Rejected):
        return {"contract": CONTRACT, "status": "blocked", "code": "files", "message": MESSAGES["files"]}
