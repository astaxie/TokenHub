/** Request outcome counts for one hour of the request health timeline. */
export type RequestHealthBucket = {
  start: string;
  total: number;
  success: number;
  warning: number;
  failure: number;
};
