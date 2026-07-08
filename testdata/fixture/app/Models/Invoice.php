<?php

namespace App\Models;

class Invoice
{
    public int $id;

    public int $subscriberId;

    public float $amount;

    public string $activatedAt;

    public function isSettled(): bool
    {
        return $this->amount <= 0.0;
    }
}
