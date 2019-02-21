-- VHDL comments are led with a double dash
ENTITY hello IS
-- No ports

END hello;

ARCHITECTURE bhv OF hello IS

BEGIN

   ASSERT FALSE
   REPORT "Hello, World"
   SEVERITY NOTE; 

END bhv;
