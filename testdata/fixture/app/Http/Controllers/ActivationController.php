<?php

namespace App\Http\Controllers;

use App\Models\Invoice;
use App\Services\Billing\ProrataCalculator;

class ActivationController
{
    public function store(array $request): Invoice
    {
        $calculator = new ProrataCalculator();

        $invoice = new Invoice();
        $invoice->subscriberId = (int) $request['subscriber_id'];
        $invoice->amount = $calculator->calculate(
            (float) $request['monthly_amount'],
            (int) $request['activation_day'],
            (int) $request['days_in_month']
        );

        return $invoice;
    }
}
