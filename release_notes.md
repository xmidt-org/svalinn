- Modified event parsing: if the eventType is state, parse the event 
  destination to find the device id.  Otherwise, take the event Source as the 
  device id.
- Added documentation in the form of updating the README and putting comments 
  in the yaml file.
- Refactored code to separate rules and requestParser into their own packages. 
  Also moved batchInserter to codex and refactored that.