function syntaxDemoMatlab
%{
A percent sign and a left brace alone on a line begin a block comment
%{
Repeating the same begins a nested block comment
%}
Ending the nested comment does NOT end the top level comment
%}
a = 1 % A percent sign indicates the rest of the line is a comment
b = 2; % Terminal semicolon is optional but suppresses echo
c = 3; d = 4; % Midline semicolon joins statements without echo
e = 5, f = 6 % Midline comma joins statements with echo
g = [',...''', '%;'] % These characters appear in other contexts too
fprintf(['Hello, ', ... Three dots continue execution on the next line
'World: %d %d %d %d %d %d %s\n'], ... AND they begin a comment
a, b, c, d, e, f,
g); % Within parentheses a terminal comma continues the line
end
